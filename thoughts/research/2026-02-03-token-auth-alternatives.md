# Token Authentication Alternatives for Issue #22

**Date**: 2026-02-03
**Status**: Research/Proposal
**Related Issue**: https://github.com/nissessenap/shepherd/issues/22

---

## Problem Analysis

The fundamental tension is:

1. **Warm-pools** = Sandboxes exist *before* tasks are assigned
2. **GitHub tokens** = Generated per-repo, short-lived (1 hour), scoped
3. **Security** = Don't want arbitrary pods calling `/token` endpoints

Issue #22's current approach adds:
- Per-task bearer token generation
- Secret storage with SHA-256 hashes
- API RBAC for Secrets
- Crash recovery logic

This is defense-in-depth, but the complexity cost is high relative to the security benefit.

---

## Creative Alternatives

### Option 1: Dual-Port Architecture

```
Port 8080: Public API
  POST /api/v1/tasks
  GET  /api/v1/tasks
  GET  /api/v1/tasks/{id}
  POST /api/v1/tasks/{id}/status

Port 8081: Runner-only API (NetworkPolicy protected)
  GET  /api/v1/tasks/{id}/data
  GET  /api/v1/tasks/{id}/token
```

**Pros:**
- Zero secrets/RBAC complexity
- NetworkPolicy is well-understood K8s primitive
- Easy to audit (just check NetworkPolicy)
- No crash recovery logic needed

**Cons:**
- NetworkPolicy support varies by CNI
- Defense relies on single control (network layer)
- Pod compromise in runner namespace = full access

**Verdict**: Viable for internal/trusted clusters. Simple, pragmatic.

---

### Option 2: Task ID as Implicit Token (Simplest)

The task ID is already a UUID. If you:
1. Make task IDs cryptographically random (already are if using `uuid.New()`)
2. Never expose task IDs outside the cluster
3. Only assign task IDs to runners via the operator POST

Then the task ID *is* the bearer token. No separate token needed.

```go
// Current: GET /api/v1/tasks/{taskID}/token
// The taskID itself IS the auth - only assigned runner knows it
```

**Pros:**
- Zero additional complexity
- No RBAC changes
- No Secrets
- Works with warm-pools (operator assigns taskID when claiming)

**Cons:**
- Task IDs visible in CRD status (but protected by K8s RBAC)
- No rotation capability
- Single-factor auth

**Verdict**: This might already be good enough. Combined with NetworkPolicy, you have defense-in-depth without code changes.

---

### Option 3: Operator-Signed JWT (No Secrets)

Operator generates a short-lived JWT at task assignment, signed with a shared key:

```yaml
# Operator and API share a static HMAC key via ConfigMap/env
TASK_AUTH_KEY: <base64 random 32 bytes>
```

```go
// Operator creates JWT when assigning task
jwt := hs256Sign({
    taskID: "task-abc",
    exp:    time.Now().Add(2*time.Hour),
}, sharedKey)

// POST to runner includes JWT
POST http://runner:8888/task
{
    "taskID": "task-abc",
    "authToken": "eyJhbG..."
}

// API validates JWT - no K8s lookup needed
func validateTaskAuth(token, taskID string) error {
    claims := hs256Verify(token, sharedKey)
    if claims.taskID != taskID { return ErrInvalid }
    if claims.exp < time.Now() { return ErrExpired }
    return nil
}
```

**Pros:**
- No RBAC for Secrets
- No K8s lookups for auth (pure compute)
- Natural expiration
- Works with warm-pools (JWT generated at assignment)
- Stateless - no crash recovery needed

**Cons:**
- Shared key rotation requires pod restarts
- JWT adds ~500 bytes per request

**Verdict**: Good middle ground. Defense-in-depth without the Secret complexity.

---

### Option 4: mTLS Between Operator/API and Runners

Each runner gets a client cert at pod creation (via cert-manager or init container):

```yaml
# SandboxTemplate injects cert-manager annotation
spec:
  template:
    metadata:
      annotations:
        cert-manager.io/inject-ca-from: shepherd/runner-ca
    spec:
      containers:
      - name: runner
        volumeMounts:
        - name: tls
          mountPath: /etc/runner-tls
```

API validates client cert CN matches expected pattern:
```go
// Only pods with valid shepherd-issued certs can connect
func tlsConfig() *tls.Config {
    return &tls.Config{
        ClientAuth: tls.RequireAndVerifyClientCert,
        ClientCAs:  shepherdCA,
    }
}
```

**Pros:**
- Strong identity - cryptographic proof of pod origin
- No bearer tokens at all
- Rotation via cert-manager
- Works at transport layer

**Cons:**
- cert-manager dependency
- Certificate lifecycle management
- More complex debugging

**Verdict**: Overkill for most cases, but worth considering if you need strong identity.

---

### Option 5: Kubernetes TokenRequest API (Built-in)

Use K8s-native service account tokens projected into pods:

```yaml
# SandboxTemplate creates pod with projected token
spec:
  template:
    spec:
      serviceAccountName: shepherd-runner
      containers:
      - name: runner
        volumeMounts:
        - name: token
          mountPath: /var/run/secrets/tokens
      volumes:
      - name: token
        projected:
          sources:
          - serviceAccountToken:
              audience: shepherd-api
              expirationSeconds: 3600
```

API validates with K8s TokenReview:
```go
// Validate service account token
func (h *handler) validateToken(token string) error {
    review := &authv1.TokenReview{
        Spec: authv1.TokenReviewSpec{
            Token:     token,
            Audiences: []string{"shepherd-api"},
        },
    }
    h.client.Create(ctx, review)
    if !review.Status.Authenticated {
        return ErrInvalid
    }
    // Optionally check review.Status.User.Username matches expected SA
    return nil
}
```

**Pros:**
- Uses K8s-native auth
- Automatic rotation
- No custom token management
- Works with warm-pools (token exists before task assignment)

**Cons:**
- All runners share same SA token (no per-task identity)
- API needs TokenReview RBAC (but it's common)
- Doesn't prove *which* task the runner is working on

**Verdict**: Good for "is this a runner?" but not "is this the right runner for this task?"

---

## Recommendations

### For MVP / Internal Clusters

**Option 2 (Task ID as implicit token) + Option 1 (Dual-port with NetworkPolicy)**

```go
// server.go changes:

// Port 8080: External API (adapters, UI)
externalRouter := chi.NewRouter()
externalRouter.Post("/tasks", handler.createTask)
externalRouter.Get("/tasks", handler.listTasks)
externalRouter.Get("/tasks/{taskID}", handler.getTask)

// Port 8081: Runner API (NetworkPolicy protected)
runnerRouter := chi.NewRouter()
runnerRouter.Get("/tasks/{taskID}/data", handler.getTaskData)
runnerRouter.Get("/tasks/{taskID}/token", handler.getTaskToken)
runnerRouter.Post("/tasks/{taskID}/status", handler.updateTaskStatus)

go http.ListenAndServe(":8080", externalRouter)
go http.ListenAndServe(":8081", runnerRouter)
```

```yaml
# NetworkPolicy: Only sandboxes can reach runner port
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  name: shepherd-api-runner-port
spec:
  podSelector:
    matchLabels:
      app: shepherd-api
  ingress:
  - from:
    - podSelector:
        matchLabels:
          sandbox.agents.x-k8s.io/managed: "true"
    ports:
    - port: 8081
```

**Security model:**
1. Task ID is unguessable (UUID)
2. Task ID only shared with assigned runner
3. Network layer blocks non-runner access to sensitive endpoints
4. Terminal task check prevents replay after completion

---

### For Production / Multi-tenant

**Option 3 (Operator-signed JWT)**

Add a single shared HMAC key (ConfigMap or env var), generate JWT at task assignment. No Secrets RBAC, stateless validation, natural expiration.

---

## What to Skip

Issue #22's approach (SHA-256 hashed tokens in Secrets) adds:
- Secrets RBAC
- Crash recovery (delete/recreate secrets)
- K8s lookups on every auth check
- Secret lifecycle management

These are real costs for modest security gains when NetworkPolicy + unguessable task IDs already provide reasonable protection.

---

## Decision Matrix

| Approach | Complexity | RBAC Changes | Warm-pool Compatible | Per-task Identity |
|----------|------------|--------------|---------------------|-------------------|
| Issue #22 (Secrets) | High | Secrets get | Yes | Yes |
| Dual-port + NetworkPolicy | Low | None | Yes | No (taskID implicit) |
| Task ID as implicit token | None | None | Yes | Yes |
| Operator-signed JWT | Medium | None | Yes | Yes |
| mTLS | High | None | Yes | No (pod identity) |
| K8s TokenRequest | Medium | TokenReview | Yes | No (SA identity) |

---

## Conclusion

Start with **dual-port + NetworkPolicy** as the MVP. If you later need per-task identity (multi-tenant, audit requirements), add **operator-signed JWTs**. Consider closing Issue #22 as "won't implement" with a rationale that NetworkPolicy provides sufficient isolation for the current threat model.
