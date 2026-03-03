# Shepherd Helm Chart Implementation Plan

## Overview

Create a single Helm chart at `charts/shepherd/` to deploy all Shepherd components: operator (controller), API server, web frontend, and optionally the GitHub adapter. The chart follows patterns from grafana-operator (CRD handling, extraObjects, RBAC from files/) and Loki (global image registry/tag overrides). CRDs are placed in `templates/` (not `crds/`) so Helm upgrades keep them current. helm-docs generates values documentation.

## Current State Analysis

- **Kustomize manifests** exist for operator, API, and web under `config/` — no GitHub adapter manifests exist
- **CRD**: single `agenttasks.toolkit.shepherd.io` at `config/crd/bases/toolkit.shepherd.io_agenttasks.yaml`
- **RBAC**: operator ClusterRole (`config/rbac/role.yaml`), API namespace Role (`config/api-rbac/role.yaml`)
- **Images**: `shepherd` (ko-built, shared by operator/api/github), `shepherd-web` (Chainguard nginx). Runner image excluded from chart.
- **nginx.conf** has hardcoded `proxy_pass http://shepherd-shepherd-api:8080` — needs ConfigMap with templated service name
- **Makefile** uses `go-install-tool` pattern for local binaries; `manifests` target runs `controller-gen` to generate CRDs

### Key Discoveries:
- Grafana-operator stores CRDs in `files/crds/`, renders them via `.Files.Glob` with `helm.sh/resource-policy: keep` (`templates/crds.yaml:1-14`)
- Grafana-operator stores RBAC rules in `files/rbac.yaml`, loaded via `.Files.Get` in `templates/rbac.yaml` — avoids duplicating rules in Helm templates
- Grafana Makefile runs `controller-gen` twice: once into `config/crd/bases`, once into `deploy/helm/.../files/crds` (`Makefile:69-70`)
- Loki's `loki.baseImage` helper: `global.image.registry` → `service.registry` → fallback, with `digest` support (`templates/_helpers.tpl:161-170`)
- Grafana-operator image line: `"{{ .Values.global.imageRegistry | default .Values.image.registry }}/{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}"` (`templates/deployment.yaml:49`)
- helm-docs comment format: `# -- description` for values, `# @default -- override` for custom defaults

## Desired End State

A working Helm chart at `charts/shepherd/` that:
- Deploys operator, API, web, and optionally GitHub adapter with proper RBAC and security contexts
- Has CRDs in `templates/` with conditional install (`crds.install: true`)
- `make manifests` automatically syncs CRDs to the Helm chart's `files/crds/`
- `make helm-docs` generates `charts/shepherd/README.md`
- `make helm-lint` validates the chart
- PDB and HPA resources exist (disabled by default) for API, web, and GitHub adapter
- All values documented with helm-docs annotations
- `helm template` renders cleanly; `helm lint` passes

### Verification:
```bash
make manifests     # syncs CRDs to charts/shepherd/files/crds/
make helm-lint     # helm template + helm lint
make helm-docs     # generates README.md
helm template test charts/shepherd/ --set githubAdapter.enabled=true | kubectl apply --dry-run=client -f -
```

## What We're NOT Doing

- No Ingress/HTTPRoute resources (deferred to future)
- No SandboxTemplate resources (user-managed)
- No runner image configuration (managed by SandboxTemplate)
- No NetworkPolicy (too diverse to template well, similar to grafana-operator's reasoning)
- No ServiceMonitor/PrometheusRule (can be added via extraObjects for now)
- No immutable CRD subchart (grafana-operator supports both; we only support mutable/templates)

## Implementation Approach

Follow grafana-operator patterns for structure and CRD handling, Loki patterns for global image configuration. Use `helm create` as starting point, strip defaults, then build up. Each component (operator, api, web, github-adapter) gets its own set of templates with a shared `_helpers.tpl`.

---

## Phase 1: Scaffold and Helpers

### Overview
Create the chart skeleton with `helm create`, remove boilerplate, establish `_helpers.tpl` with global image helpers and common label patterns.

### Changes Required:

#### 1. Create chart scaffold
```bash
cd charts && helm create shepherd
```

Then **remove** these generated files:
- `charts/shepherd/templates/deployment.yaml`
- `charts/shepherd/templates/service.yaml`
- `charts/shepherd/templates/serviceaccount.yaml`
- `charts/shepherd/templates/ingress.yaml`
- `charts/shepherd/templates/hpa.yaml`
- `charts/shepherd/templates/NOTES.txt`
- `charts/shepherd/templates/tests/`
- `charts/shepherd/.helmignore` (recreate with simpler content)

#### 2. Chart.yaml
**File**: `charts/shepherd/Chart.yaml`
```yaml
apiVersion: v2
name: shepherd
description: Background coding agent orchestrator for Kubernetes
type: application
version: 0.1.0
appVersion: "0.1.0"
kubeVersion: ">=1.28.0-0"
home: https://github.com/NissesSenap/shepherd
sources:
  - https://github.com/NissesSenap/shepherd
maintainers:
  - name: NissesSenap
    url: https://github.com/NissesSenap
```

#### 3. .helmignore
**File**: `charts/shepherd/.helmignore`
```
.DS_Store
*.tgz
.git/
.gitignore
```

#### 4. _helpers.tpl
**File**: `charts/shepherd/templates/_helpers.tpl`

Key helpers following grafana-operator + Loki patterns:

```gotemplate
{{/*
Expand the name of the chart.
*/}}
{{- define "shepherd.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "shepherd.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Allow the release namespace to be overridden
*/}}
{{- define "shepherd.namespace" -}}
{{ .Values.namespaceOverride | default .Release.Namespace }}
{{- end -}}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "shepherd.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "shepherd.labels" -}}
helm.sh/chart: {{ include "shepherd.chart" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: shepherd
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
{{- with .Values.global.additionalLabels }}
{{ toYaml . }}
{{- end }}
{{- end }}

{{/*
Component labels - call with dict "context" . "component" "api"
*/}}
{{- define "shepherd.componentLabels" -}}
{{ include "shepherd.labels" .context }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Component selector labels - call with dict "context" . "component" "api"
*/}}
{{- define "shepherd.componentSelectorLabels" -}}
app.kubernetes.io/name: {{ include "shepherd.name" .context }}
app.kubernetes.io/instance: {{ .context.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end }}

{{/*
Base image reference (Loki pattern).
Usage: {{ include "shepherd.image" (dict "service" .Values.operator.image "global" .Values.global.image "defaultVersion" .Chart.AppVersion) }}
*/}}
{{- define "shepherd.image" -}}
{{- $registry := .global.registry | default .service.registry | default "" -}}
{{- $repository := .service.repository | default "" -}}
{{- $tag := .service.tag | default .defaultVersion | toString -}}
{{- if $registry -}}
  {{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
  {{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end -}}

{{/*
Create the name of the service account for a component
Usage: {{ include "shepherd.serviceAccountName" (dict "context" . "component" "api" "sa" .Values.api.serviceAccount) }}
*/}}
{{- define "shepherd.serviceAccountName" -}}
{{- if .sa.create }}
{{- default (printf "%s-%s" (include "shepherd.fullname" .context) .component) .sa.name }}
{{- else }}
{{- default "default" .sa.name }}
{{- end }}
{{- end }}
```

#### 5. Initial values.yaml (global + name overrides only for Phase 1)
**File**: `charts/shepherd/values.yaml`

```yaml
# -- Overrides the chart name
# @default -- chart name
nameOverride: ""

# -- Overrides the fully qualified app name
# @default -- derived from release name + chart name
fullnameOverride: ""

# -- Override the release namespace
# @default -- .Release.Namespace
namespaceOverride: ""

global:
  image:
    # -- Global image registry override for all Shepherd images
    registry: ""
  # -- Additional labels applied to all resources
  additionalLabels: {}
  # -- Image pull secrets shared across all components
  imagePullSecrets: []
```

### Success Criteria:

#### Automated Verification:
- [x] `helm template test charts/shepherd/` renders without errors (will be minimal output)
- [x] `helm lint charts/shepherd/` passes

#### Manual Verification:
- [ ] Chart directory structure looks clean — no leftover boilerplate files

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the next phase.

---

## Phase 2: Operator (Controller) Templates

### Overview
Add the operator deployment, RBAC (ClusterRole from `files/rbac.yaml`), service account, and leader election role. The operator is a single-replica controller using `shepherd operator` subcommand.

### Changes Required:

#### 1. Copy RBAC rules to files/
**File**: `charts/shepherd/files/rbac.yaml`
Copy of `config/rbac/role.yaml` (just the rules, loaded via `.Files.Get`). This will be auto-synced by the Makefile target (Phase 7).

#### 2. Operator Deployment
**File**: `charts/shepherd/templates/operator/deployment.yaml`

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ include "shepherd.fullname" . }}-operator
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "operator") | nindent 4 }}
  {{- with .Values.operator.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
spec:
  replicas: 1
  selector:
    matchLabels:
      {{- include "shepherd.componentSelectorLabels" (dict "context" . "component" "operator") | nindent 6 }}
  template:
    metadata:
      {{- with .Values.operator.podAnnotations }}
      annotations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      labels:
        {{- include "shepherd.componentLabels" (dict "context" . "component" "operator") | nindent 8 }}
        {{- with .Values.operator.podLabels }}
        {{- toYaml . | nindent 8 }}
        {{- end }}
    spec:
      {{- with (coalesce .Values.operator.imagePullSecrets .Values.global.imagePullSecrets) }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      serviceAccountName: {{ include "shepherd.serviceAccountName" (dict "context" . "component" "operator" "sa" .Values.operator.serviceAccount) }}
      securityContext:
        {{- toYaml .Values.operator.podSecurityContext | nindent 8 }}
      containers:
        - name: operator
          securityContext:
            {{- toYaml .Values.operator.securityContext | nindent 12 }}
          image: {{ include "shepherd.image" (dict "service" .Values.operator.image "global" .Values.global.image "defaultVersion" .Chart.AppVersion) }}
          imagePullPolicy: {{ .Values.operator.image.pullPolicy }}
          command:
            - /ko-app/shepherd
          args:
            - operator
            {{- if .Values.operator.leaderElection }}
            - --leader-election
            {{- end }}
            - --health-addr=:{{ .Values.operator.healthPort }}
            - --metrics-addr=:{{ .Values.operator.metricsPort }}
            - --apiurl={{ printf "http://%s-api.%s.svc.cluster.local:%d" (include "shepherd.fullname" .) (include "shepherd.namespace" .) (.Values.api.service.internalPort | int) }}
          ports:
            - name: health
              containerPort: {{ .Values.operator.healthPort }}
              protocol: TCP
            - name: metrics
              containerPort: {{ .Values.operator.metricsPort }}
              protocol: TCP
          livenessProbe:
            httpGet:
              path: /healthz
              port: health
            initialDelaySeconds: 15
            periodSeconds: 20
          readinessProbe:
            httpGet:
              path: /readyz
              port: health
            initialDelaySeconds: 5
            periodSeconds: 10
          {{- with .Values.operator.resources }}
          resources:
            {{- toYaml . | nindent 12 }}
          {{- end }}
      terminationGracePeriodSeconds: 10
      {{- with .Values.operator.nodeSelector }}
      nodeSelector:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.operator.affinity }}
      affinity:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- with .Values.operator.tolerations }}
      tolerations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
```

#### 3. Operator ServiceAccount
**File**: `charts/shepherd/templates/operator/serviceaccount.yaml`

```yaml
{{- if .Values.operator.serviceAccount.create -}}
apiVersion: v1
kind: ServiceAccount
metadata:
  name: {{ include "shepherd.serviceAccountName" (dict "context" . "component" "operator" "sa" .Values.operator.serviceAccount) }}
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "operator") | nindent 4 }}
  {{- with .Values.operator.serviceAccount.annotations }}
  annotations:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
```

#### 4. Operator RBAC
**File**: `charts/shepherd/templates/operator/rbac.yaml`

Follows grafana-operator pattern: load rules from `files/rbac.yaml` via `.Files.Get`.

```yaml
{{- if .Values.operator.rbac.create -}}
{{- $rbac := .Files.Get "files/rbac.yaml" | fromYaml }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: {{ include "shepherd.fullname" . }}-operator
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "operator") | nindent 4 }}
rules:
  {{- toYaml $rbac.rules | nindent 2 }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: {{ include "shepherd.fullname" . }}-operator
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "operator") | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: {{ include "shepherd.fullname" . }}-operator
subjects:
  - kind: ServiceAccount
    name: {{ include "shepherd.serviceAccountName" (dict "context" . "component" "operator" "sa" .Values.operator.serviceAccount) }}
    namespace: {{ include "shepherd.namespace" . }}
{{- if .Values.operator.leaderElection }}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ include "shepherd.fullname" . }}-operator-leases
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "operator") | nindent 4 }}
rules:
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ include "shepherd.fullname" . }}-operator-leases
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "operator") | nindent 4 }}
subjects:
  - kind: ServiceAccount
    name: {{ include "shepherd.serviceAccountName" (dict "context" . "component" "operator" "sa" .Values.operator.serviceAccount) }}
    namespace: {{ include "shepherd.namespace" . }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ include "shepherd.fullname" . }}-operator-leases
{{- end }}
{{- end }}
```

Note: The `files/rbac.yaml` already contains lease rules in the ClusterRole. The separate leader-election Role provides namespace-scoped lease access which is a more restricted alternative. We keep the ClusterRole lease rules because `controller-gen` generates them there, and the namespace Role for tighter lease binding. This mirrors the kubebuilder/kustomize split (`role.yaml` + `leader_election_role.yaml`). An alternative is to strip lease rules from the ClusterRole in the sync script — we can revisit.

#### 5. Values for operator section

Add to `values.yaml`:

```yaml
operator:
  # -- Annotations for the operator deployment
  annotations: {}
  # -- Labels for the operator pods
  podLabels: {}
  # -- Annotations for the operator pods
  podAnnotations: {}
  # -- Enable leader election for the operator
  leaderElection: true
  # -- Health probe port
  healthPort: 8082
  # -- Metrics port
  metricsPort: 9090
  image:
    # -- Operator image registry
    registry: ghcr.io
    # -- Operator image repository
    repository: nissessenap/shepherd
    # -- Operator image tag (defaults to chart appVersion)
    # @default -- .Chart.AppVersion
    tag: ""
    # -- Operator image pull policy
    pullPolicy: IfNotPresent
  # -- Image pull secrets for operator (overrides global)
  imagePullSecrets: []
  serviceAccount:
    # -- Whether to create a service account for the operator
    create: true
    # -- Annotations to add to the operator service account
    annotations: {}
    # -- The name of the operator service account
    # @default -- fullname-operator
    name: ""
  rbac:
    # -- Whether to create RBAC resources for the operator
    create: true
  # -- Pod security context for the operator
  podSecurityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  # -- Container security context for the operator
  securityContext:
    readOnlyRootFilesystem: true
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
  # -- Resource requests and limits for the operator
  resources:
    limits:
      cpu: 500m
      memory: 128Mi
    requests:
      cpu: 10m
      memory: 64Mi
  # -- Node selector for the operator pods
  nodeSelector: {}
  # -- Tolerations for the operator pods
  tolerations: []
  # -- Affinity rules for the operator pods
  affinity: {}
```

### Success Criteria:

#### Automated Verification:
- [x] `helm template test charts/shepherd/` renders operator deployment, SA, ClusterRole, ClusterRoleBinding
- [x] `helm lint charts/shepherd/` passes
- [x] Rendered operator deployment has correct security contexts and args

#### Manual Verification:
- [ ] RBAC rules in rendered output match `config/rbac/role.yaml`
- [ ] API URL in operator args correctly references the API service name

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the next phase.

---

## Phase 3: API Server Templates

### Overview
Add the API server deployment, namespace-scoped RBAC, service (public + internal ports), and service account. The API uses the `shepherd api` subcommand and needs a GitHub App secret for token generation.

### Changes Required:

#### 1. API Deployment
**File**: `charts/shepherd/templates/api/deployment.yaml`

The API deployment mounts a GitHub App private key from a Secret. The Secret itself is NOT created by the chart (user provides it), but its name is configurable.

Key env vars:
- `SHEPHERD_NAMESPACE` via downward API
- `SHEPHERD_GITHUB_APP_ID`, `SHEPHERD_GITHUB_INSTALLATION_ID` from secret
- `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` pointing to mounted key

The GitHub App env vars should be conditional — only set when `api.githubApp.enabled` is true (matching the existing kustomize behavior where all three are all-or-nothing).

#### 2. API Service
**File**: `charts/shepherd/templates/api/service.yaml`

Two ports: `api` (8080, public) and `internal` (8081, runner-facing).

#### 3. API ServiceAccount
**File**: `charts/shepherd/templates/api/serviceaccount.yaml`

Same pattern as operator.

#### 4. API RBAC
**File**: `charts/shepherd/templates/api/rbac.yaml`

Namespace-scoped Role and RoleBinding for agenttasks CRUD.

```yaml
{{- if .Values.api.rbac.create -}}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {{ include "shepherd.fullname" . }}-api
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "api") | nindent 4 }}
rules:
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks"]
    verbs: ["get", "list", "watch", "create"]
  - apiGroups: ["toolkit.shepherd.io"]
    resources: ["agenttasks/status"]
    verbs: ["get", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: {{ include "shepherd.fullname" . }}-api
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "api") | nindent 4 }}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: Role
  name: {{ include "shepherd.fullname" . }}-api
subjects:
  - kind: ServiceAccount
    name: {{ include "shepherd.serviceAccountName" (dict "context" . "component" "api" "sa" .Values.api.serviceAccount) }}
    namespace: {{ include "shepherd.namespace" . }}
{{- end }}
```

#### 5. Values for api section

```yaml
api:
  # -- Number of API server replicas
  replicas: 2
  # -- Annotations for the API deployment
  annotations: {}
  # -- Labels for the API pods
  podLabels: {}
  # -- Annotations for the API pods
  podAnnotations: {}
  image:
    # -- API image registry
    registry: ghcr.io
    # -- API image repository (same binary as operator)
    repository: nissessenap/shepherd
    # -- API image tag (defaults to chart appVersion)
    # @default -- .Chart.AppVersion
    tag: ""
    # -- API image pull policy
    pullPolicy: IfNotPresent
  # -- Image pull secrets for the API (overrides global)
  imagePullSecrets: []
  serviceAccount:
    # -- Whether to create a service account for the API
    create: true
    # -- Annotations to add to the API service account
    annotations: {}
    # -- The name of the API service account
    # @default -- fullname-api
    name: ""
  rbac:
    # -- Whether to create RBAC resources for the API
    create: true
  service:
    # -- API service type
    type: ClusterIP
    # -- Public API port
    port: 8080
    # -- Internal API port (for runner communication)
    internalPort: 8081
    # -- Annotations for the API service
    annotations: {}
  githubApp:
    # -- Enable GitHub App integration for token generation
    enabled: false
    # -- Name of the existing Secret containing GitHub App credentials.
    # Must contain keys: app-id, installation-id, private-key
    existingSecret: ""
  # -- Pod security context for the API
  podSecurityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  # -- Container security context for the API
  securityContext:
    readOnlyRootFilesystem: true
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
  # -- Resource requests and limits for the API
  resources:
    limits:
      cpu: 500m
      memory: 128Mi
    requests:
      cpu: 10m
      memory: 64Mi
  # -- Node selector for the API pods
  nodeSelector: {}
  # -- Tolerations for the API pods
  tolerations: []
  # -- Affinity rules for the API pods
  affinity: {}
```

### Success Criteria:

#### Automated Verification:
- [x] `helm template test charts/shepherd/` renders API deployment, service, SA, Role, RoleBinding
- [x] `helm template test charts/shepherd/ --set api.githubApp.enabled=true --set api.githubApp.existingSecret=my-secret` renders with secret volume mounts
- [x] `helm lint charts/shepherd/` passes

#### Manual Verification:
- [ ] API service has both public (8080) and internal (8081) ports
- [ ] Without `githubApp.enabled`, no secret volumes are mounted
- [ ] Operator deployment's `--apiurl` arg references API service internal port correctly

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the next phase.

---

## Phase 4: Web Frontend Templates

### Overview
Add the web frontend deployment with a ConfigMap-based nginx.conf (templated API service name), service, and service account.

### Changes Required:

#### 1. Nginx ConfigMap
**File**: `charts/shepherd/templates/web/configmap-nginx.yaml`

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ include "shepherd.fullname" . }}-web-nginx
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "web") | nindent 4 }}
data:
  nginx.conf: |
    server {
        listen 8080;
        root /usr/share/nginx/html;
        index index.html;

        location / {
            try_files $uri $uri/ /index.html;
        }

        location /assets/ {
            expires 1y;
            add_header Cache-Control "public, immutable";
        }

        location /api/ {
            proxy_pass http://{{ include "shepherd.fullname" . }}-api:{{ .Values.api.service.port }};
            proxy_http_version 1.1;
            proxy_set_header Upgrade $http_upgrade;
            proxy_set_header Connection "upgrade";
        }
    }
```

#### 2. Web Deployment
**File**: `charts/shepherd/templates/web/deployment.yaml`

Mounts the nginx ConfigMap, overriding the baked-in config. The volume mount path is `/etc/nginx/conf.d/nginx.default.conf` matching `build/web/Dockerfile:11`.

Note: Chainguard nginx runs as non-root (uid 65532). The `readOnlyRootFilesystem` may need to be false for nginx (it writes to `/var/run/nginx.pid` and `/var/lib/nginx/tmp/`). We'll set it to false by default for the web container and add `emptyDir` tmpfs mounts for the nginx temp directories.

```yaml
# Volumes:
volumes:
  - name: nginx-config
    configMap:
      name: {{ include "shepherd.fullname" . }}-web-nginx
  - name: nginx-tmp
    emptyDir: {}
  - name: nginx-run
    emptyDir: {}

# Volume mounts:
volumeMounts:
  - name: nginx-config
    mountPath: /etc/nginx/conf.d/nginx.default.conf
    subPath: nginx.conf
    readOnly: true
  - name: nginx-tmp
    mountPath: /var/lib/nginx/tmp
  - name: nginx-run
    mountPath: /var/run
```

This allows `readOnlyRootFilesystem: true` on the web container while nginx can still write its pid and temp files.

#### 3. Web Service
**File**: `charts/shepherd/templates/web/service.yaml`

Single port: `http` (8080).

#### 4. Web ServiceAccount
**File**: `charts/shepherd/templates/web/serviceaccount.yaml`

Same pattern. The web container doesn't need K8s API access, but having a dedicated SA is still best practice (automountServiceAccountToken can be set to false).

#### 5. Values for web section

```yaml
web:
  # -- Number of web frontend replicas
  replicas: 1
  # -- Annotations for the web deployment
  annotations: {}
  # -- Labels for the web pods
  podLabels: {}
  # -- Annotations for the web pods
  podAnnotations: {}
  image:
    # -- Web image registry
    registry: ghcr.io
    # -- Web image repository
    repository: nissessenap/shepherd-web
    # -- Web image tag (defaults to chart appVersion)
    # @default -- .Chart.AppVersion
    tag: ""
    # -- Web image pull policy
    pullPolicy: IfNotPresent
  # -- Image pull secrets for the web frontend (overrides global)
  imagePullSecrets: []
  serviceAccount:
    # -- Whether to create a service account for the web frontend
    create: true
    # -- Annotations to add to the web service account
    annotations: {}
    # -- The name of the web service account
    # @default -- fullname-web
    name: ""
    # -- Whether to auto-mount the service account token (not needed for web)
    automountServiceAccountToken: false
  service:
    # -- Web service type
    type: ClusterIP
    # -- Web service port
    port: 8080
    # -- Annotations for the web service
    annotations: {}
  # -- Pod security context for the web frontend
  podSecurityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  # -- Container security context for the web frontend
  securityContext:
    allowPrivilegeEscalation: false
    readOnlyRootFilesystem: true
    capabilities:
      drop:
        - ALL
  # -- Resource requests and limits for the web frontend
  resources:
    limits:
      cpu: 100m
      memory: 64Mi
    requests:
      cpu: 10m
      memory: 32Mi
  # -- Node selector for the web pods
  nodeSelector: {}
  # -- Tolerations for the web pods
  tolerations: []
  # -- Affinity rules for the web pods
  affinity: {}
```

### Success Criteria:

#### Automated Verification:
- [x] `helm template test charts/shepherd/` renders web deployment, ConfigMap, service, SA
- [x] ConfigMap contains correct `proxy_pass` URL referencing the API service
- [x] `helm lint charts/shepherd/` passes

#### Manual Verification:
- [ ] nginx.conf in ConfigMap uses templated service name, not hardcoded
- [ ] Web deployment mounts the ConfigMap at the correct path
- [ ] emptyDir volumes for nginx tmp/run directories are present

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the next phase.

---

## Phase 5: CRDs

### Overview
Add CRD files and the template that renders them conditionally. Set up the Makefile target to automatically sync CRDs from `controller-gen` output to the Helm chart.

### Changes Required:

#### 1. Copy CRD to files/crds/
**File**: `charts/shepherd/files/crds/toolkit.shepherd.io_agenttasks.yaml`

Copy of `config/crd/bases/toolkit.shepherd.io_agenttasks.yaml`. This will be auto-synced by the Makefile.

#### 2. CRD template
**File**: `charts/shepherd/templates/crds.yaml`

Following grafana-operator pattern with `helm.sh/resource-policy: keep` to protect CRDs on chart uninstall:

```yaml
{{- if .Values.crds.install }}
  {{- range $path, $_ := .Files.Glob "files/crds/*.yaml" }}
    {{- $crds := regexSplit "^---$" ($.Files.Get $path) -1 }}
    {{- range $crds }}
      {{- $crd := . | fromYaml }}
      {{- $_ := set $crd.metadata "annotations" (default dict $crd.metadata.annotations) }}
      {{- $_ := set $crd.metadata.annotations "helm.sh/resource-policy" "keep" }}
      {{- range $key, $value := $crd }}
        {{- $key }}: {{ $value | toRawJson }}
        {{- print "\n" }}
      {{- end }}
---
    {{- end }}
  {{- end }}
{{- end }}
```

#### 3. Values for CRDs

```yaml
crds:
  # -- Whether to install CRDs with the chart.
  # CRDs are placed in templates/ (not crds/) so they are updated on helm upgrade.
  # Protected with helm.sh/resource-policy: keep to prevent deletion on chart uninstall.
  install: true
```

#### 4. Makefile changes

Add to the `manifests` target to sync CRDs to the helm chart, following grafana-operator pattern:

```makefile
.PHONY: manifests
manifests: controller-gen ## Generate WebhookConfiguration, ClusterRole and CustomResourceDefinition objects.
	"$(CONTROLLER_GEN)" rbac:roleName=manager-role crd webhook paths="$$(go list ./... | paste -sd';' -)" output:crd:artifacts:config=config/crd/bases
	@# Sync CRDs to Helm chart
	@mkdir -p charts/shepherd/files/crds
	@cp config/crd/bases/*.yaml charts/shepherd/files/crds/
	@echo "Synced CRDs to charts/shepherd/files/crds/"
	@# Sync RBAC to Helm chart
	@cp config/rbac/role.yaml charts/shepherd/files/rbac.yaml
	@echo "Synced RBAC to charts/shepherd/files/rbac.yaml"
```

### Success Criteria:

#### Automated Verification:
- [x] `make manifests` generates CRDs in both `config/crd/bases/` and `charts/shepherd/files/crds/`
- [x] `make manifests` syncs RBAC to `charts/shepherd/files/rbac.yaml`
- [x] `helm template test charts/shepherd/` renders the CRD with `helm.sh/resource-policy: keep`
- [x] `helm template test charts/shepherd/ --set crds.install=false` does NOT render the CRD
- [x] `helm lint charts/shepherd/` passes

#### Manual Verification:
- [ ] CRD YAML in `charts/shepherd/files/crds/` matches `config/crd/bases/`
- [ ] `files/rbac.yaml` matches `config/rbac/role.yaml`

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the next phase.

---

## Phase 6: GitHub Adapter (Optional Component)

### Overview
Add the GitHub adapter as an optional component (disabled by default). This component has no kustomize manifests today, so we build it from scratch based on the CLI flags in `cmd/shepherd/main.go:40-80`.

### Changes Required:

#### 1. GitHub Adapter Deployment
**File**: `charts/shepherd/templates/github-adapter/deployment.yaml`

Wrapped in `{{- if .Values.githubAdapter.enabled }}`.

The adapter needs:
- `SHEPHERD_GITHUB_WEBHOOK_SECRET` from secret
- `SHEPHERD_GITHUB_APP_ID` from secret
- `SHEPHERD_GITHUB_INSTALLATION_ID` from secret
- `SHEPHERD_GITHUB_PRIVATE_KEY_PATH` mounted from secret
- `SHEPHERD_API_URL` pointing to the API service internal port
- `SHEPHERD_CALLBACK_SECRET` from secret (optional)
- `SHEPHERD_CALLBACK_URL` from values

All GitHub credentials come from an existing secret (same pattern as API).

#### 2. GitHub Adapter Service
**File**: `charts/shepherd/templates/github-adapter/service.yaml`

Single port for webhook reception. Wrapped in `{{- if .Values.githubAdapter.enabled }}`.

#### 3. GitHub Adapter ServiceAccount
**File**: `charts/shepherd/templates/github-adapter/serviceaccount.yaml`

The GitHub adapter doesn't need K8s API access, so `automountServiceAccountToken: false`.

#### 4. Values for githubAdapter section

```yaml
githubAdapter:
  # -- Enable the GitHub adapter component
  enabled: false
  # -- Number of GitHub adapter replicas
  replicas: 1
  # -- Annotations for the GitHub adapter deployment
  annotations: {}
  # -- Labels for the GitHub adapter pods
  podLabels: {}
  # -- Annotations for the GitHub adapter pods
  podAnnotations: {}
  image:
    # -- GitHub adapter image registry
    registry: ghcr.io
    # -- GitHub adapter image repository (same binary as operator)
    repository: nissessenap/shepherd
    # -- GitHub adapter image tag (defaults to chart appVersion)
    # @default -- .Chart.AppVersion
    tag: ""
    # -- GitHub adapter image pull policy
    pullPolicy: IfNotPresent
  # -- Image pull secrets for the GitHub adapter (overrides global)
  imagePullSecrets: []
  serviceAccount:
    # -- Whether to create a service account for the GitHub adapter
    create: true
    # -- Annotations to add to the GitHub adapter service account
    annotations: {}
    # -- The name of the GitHub adapter service account
    # @default -- fullname-github-adapter
    name: ""
    # -- Whether to auto-mount the service account token (not needed)
    automountServiceAccountToken: false
  service:
    # -- GitHub adapter service type
    type: ClusterIP
    # -- GitHub adapter webhook port
    port: 8082
    # -- Annotations for the GitHub adapter service
    annotations: {}
  # -- Name of the existing Secret containing GitHub App credentials.
  # Must contain keys: webhook-secret, app-id, installation-id, private-key.
  # Optionally: callback-secret.
  existingSecret: ""
  # -- Callback URL that the API server will call back to
  callbackURL: ""
  # -- Default sandbox template name for new tasks
  defaultSandboxTemplate: "default"
  # -- Pod security context for the GitHub adapter
  podSecurityContext:
    runAsNonRoot: true
    seccompProfile:
      type: RuntimeDefault
  # -- Container security context for the GitHub adapter
  securityContext:
    readOnlyRootFilesystem: true
    allowPrivilegeEscalation: false
    capabilities:
      drop:
        - ALL
  # -- Resource requests and limits for the GitHub adapter
  resources:
    limits:
      cpu: 500m
      memory: 128Mi
    requests:
      cpu: 10m
      memory: 64Mi
  # -- Node selector for the GitHub adapter pods
  nodeSelector: {}
  # -- Tolerations for the GitHub adapter pods
  tolerations: []
  # -- Affinity rules for the GitHub adapter pods
  affinity: {}
```

### Success Criteria:

#### Automated Verification:
- [x] `helm template test charts/shepherd/` does NOT render GitHub adapter resources (disabled by default)
- [x] `helm template test charts/shepherd/ --set githubAdapter.enabled=true --set githubAdapter.existingSecret=gh-secret --set githubAdapter.callbackURL=https://example.com/callback` renders all GitHub adapter resources
- [x] `helm lint charts/shepherd/` passes

#### Manual Verification:
- [ ] GitHub adapter deployment has correct env vars and volume mounts
- [ ] API URL in adapter points to API internal service port
- [ ] No RBAC created (adapter doesn't need K8s API access)

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the next phase.

---

## Phase 7: PDB and HPA

### Overview
Add PodDisruptionBudget and HorizontalPodAutoscaler for API, web, and GitHub adapter (all disabled by default). Not for the operator (single-replica controller with leader election).

### Changes Required:

#### 1. PDB template (one per component)

**Files**:
- `charts/shepherd/templates/api/pdb.yaml`
- `charts/shepherd/templates/web/pdb.yaml`
- `charts/shepherd/templates/github-adapter/pdb.yaml`

Pattern (API example):
```yaml
{{- if .Values.api.pdb.enabled }}
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ include "shepherd.fullname" . }}-api
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "api") | nindent 4 }}
spec:
  {{- with .Values.api.pdb.minAvailable }}
  minAvailable: {{ . }}
  {{- end }}
  {{- with .Values.api.pdb.maxUnavailable }}
  maxUnavailable: {{ . }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "shepherd.componentSelectorLabels" (dict "context" . "component" "api") | nindent 6 }}
{{- end }}
```

GitHub adapter PDB wraps in `{{- if and .Values.githubAdapter.enabled .Values.githubAdapter.pdb.enabled }}`.

#### 2. HPA template (one per component)

**Files**:
- `charts/shepherd/templates/api/hpa.yaml`
- `charts/shepherd/templates/web/hpa.yaml`
- `charts/shepherd/templates/github-adapter/hpa.yaml`

Pattern (API example):
```yaml
{{- if .Values.api.hpa.enabled }}
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: {{ include "shepherd.fullname" . }}-api
  namespace: {{ include "shepherd.namespace" . }}
  labels:
    {{- include "shepherd.componentLabels" (dict "context" . "component" "api") | nindent 4 }}
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: {{ include "shepherd.fullname" . }}-api
  minReplicas: {{ .Values.api.hpa.minReplicas }}
  maxReplicas: {{ .Values.api.hpa.maxReplicas }}
  metrics:
    {{- toYaml .Values.api.hpa.metrics | nindent 4 }}
{{- end }}
```

#### 3. Values for PDB and HPA (add to each component section)

```yaml
# Added to api:, web:, and githubAdapter: sections
  pdb:
    # -- Enable PodDisruptionBudget for [component]
    enabled: false
    # -- Minimum available pods (mutually exclusive with maxUnavailable)
    minAvailable: 1
    # -- Maximum unavailable pods (mutually exclusive with minAvailable)
    # @default -- not set
    maxUnavailable: ""
  hpa:
    # -- Enable HorizontalPodAutoscaler for [component]
    enabled: false
    # -- Minimum number of replicas
    minReplicas: 2
    # -- Maximum number of replicas
    maxReplicas: 5
    # -- Metrics for the HPA
    metrics:
      - type: Resource
        resource:
          name: cpu
          target:
            type: Utilization
            averageUtilization: 80
```

### Success Criteria:

#### Automated Verification:
- [x] `helm template test charts/shepherd/` does NOT render any PDB or HPA (disabled by default)
- [x] `helm template test charts/shepherd/ --set api.pdb.enabled=true --set api.hpa.enabled=true` renders PDB and HPA for API
- [x] `helm template test charts/shepherd/ --set githubAdapter.enabled=true --set githubAdapter.pdb.enabled=true` renders PDB for GitHub adapter
- [x] `helm lint charts/shepherd/` passes

#### Manual Verification:
- [ ] PDB selector labels match deployment selector labels
- [ ] HPA scaleTargetRef name matches deployment name

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the next phase.

---

## Phase 8: Extra Objects, helm-docs, and Polish

### Overview
Add extraObjects template, README.md.gotmpl, helm-docs Makefile target, helm-lint target, and final values.yaml polish.

### Changes Required:

#### 1. Extra Objects template
**File**: `charts/shepherd/templates/extraobjects.yaml`

```yaml
{{ range .Values.extraObjects }}
---
{{ tpl (toYaml .) $ }}
{{ end }}
```

#### 2. Values for extraObjects

```yaml
# -- Array of extra K8s objects to deploy (supports templating)
extraObjects: []
# - apiVersion: monitoring.coreos.com/v1
#   kind: ServiceMonitor
#   metadata:
#     name: shepherd-api
#   spec:
#     selector:
#       matchLabels:
#         app.kubernetes.io/component: api
#     endpoints:
#       - port: metrics
```

#### 3. README.md.gotmpl
**File**: `charts/shepherd/README.md.gotmpl`

```gotemplate
{{ template "chart.header" . }}

{{ template "chart.typeBadge" . }}{{ template "chart.appVersionBadge" . }}

{{ template "chart.description" . }}

## Prerequisites

- Kubernetes >= 1.28
- Helm >= 3.10
- [agent-sandbox operator](https://github.com/kubernetes-sigs/agent-sandbox) installed in the cluster

## Installation

```shell
helm install shepherd charts/shepherd/
```

### With GitHub Adapter

```shell
helm install shepherd charts/shepherd/ \
  --set githubAdapter.enabled=true \
  --set githubAdapter.existingSecret=my-github-secret \
  --set githubAdapter.callbackURL=https://my-domain.com/callback
```

## CRDs

CRDs are installed via `templates/` (not `crds/`) so they are automatically updated on `helm upgrade`.
They are protected with `helm.sh/resource-policy: keep` to prevent deletion on chart uninstall.

To skip CRD installation (e.g. if managed externally):
```shell
helm install shepherd charts/shepherd/ --set crds.install=false
```

{{ template "chart.maintainersSection" . }}

{{ template "chart.sourcesSection" . }}

{{ template "chart.requirementsSection" . }}

{{ template "chart.valuesSection" . }}

{{ template "helm-docs.versionFooter" . }}
```

#### 4. Makefile targets

Add to `Makefile`:

```makefile
## Tool Binaries (add to existing section)
HELM_DOCS ?= $(LOCALBIN)/helm-docs
HELM ?= helm

## Tool Versions (add to existing section)
HELM_DOCS_VERSION ?= v1.14.2

##@ Helm

.PHONY: helm-docs
helm-docs: $(HELM_DOCS) ## Generate Helm chart documentation.
	$(HELM_DOCS) --chart-search-root=charts

.PHONY: helm-lint
helm-lint: ## Validate Helm chart.
	$(HELM) template charts/shepherd/ > /dev/null
	$(HELM) lint charts/shepherd/

.PHONY: helm-docs-tool
helm-docs-tool: $(HELM_DOCS) ## Download helm-docs locally if necessary.
$(HELM_DOCS): $(LOCALBIN)
	$(call go-install-tool,$(HELM_DOCS),github.com/norwoodj/helm-docs/cmd/helm-docs,$(HELM_DOCS_VERSION))
```

Also update the `manifests` target (already described in Phase 5).

### Success Criteria:

#### Automated Verification:
- [ ] `make helm-lint` passes (helm template + helm lint)
- [ ] `make helm-docs` generates `charts/shepherd/README.md`
- [ ] `make manifests` syncs CRDs and RBAC to Helm chart
- [ ] `helm template test charts/shepherd/ --set extraObjects[0].apiVersion=v1 --set extraObjects[0].kind=ConfigMap --set extraObjects[0].metadata.name=test` renders the extra object
- [ ] Full `helm template` with all options enabled renders cleanly:
  ```bash
  helm template test charts/shepherd/ \
    --set githubAdapter.enabled=true \
    --set githubAdapter.existingSecret=gh-secret \
    --set githubAdapter.callbackURL=https://example.com/callback \
    --set api.githubApp.enabled=true \
    --set api.githubApp.existingSecret=api-secret \
    --set api.pdb.enabled=true \
    --set api.hpa.enabled=true \
    --set web.pdb.enabled=true \
    --set web.hpa.enabled=true \
    --set githubAdapter.pdb.enabled=true \
    --set githubAdapter.hpa.enabled=true
  ```

#### Manual Verification:
- [ ] Generated README.md has a values table with all documented values
- [ ] All values have helm-docs descriptions (`# --`)
- [ ] Security contexts are consistent across all components (restricted pod security standard)
- [ ] Chart structure looks clean and follows grafana-operator conventions

**Implementation Note**: After completing this phase, the Helm chart is complete and ready for use.

---

## Testing Strategy

### Automated:
- `make helm-lint` — validates chart renders and lints
- `make helm-docs` — keeps docs in sync (CI can verify no diff)
- `helm template` with various value combinations to verify conditional rendering

### Manual Testing Steps:
1. Deploy to a Kind cluster with agent-sandbox operator installed
2. Verify all pods come up healthy
3. Verify the operator can reconcile AgentTask resources
4. Verify the web frontend proxies API requests correctly through the ConfigMap nginx config
5. Enable GitHub adapter and verify webhook reception

## File Tree (Final)

```
charts/shepherd/
├── .helmignore
├── Chart.yaml
├── README.md                          # generated by helm-docs
├── README.md.gotmpl                   # template for helm-docs
├── values.yaml
├── files/
│   ├── rbac.yaml                      # synced from config/rbac/role.yaml
│   └── crds/
│       └── toolkit.shepherd.io_agenttasks.yaml  # synced from config/crd/bases/
└── templates/
    ├── _helpers.tpl
    ├── crds.yaml
    ├── extraobjects.yaml
    ├── operator/
    │   ├── deployment.yaml
    │   ├── serviceaccount.yaml
    │   └── rbac.yaml
    ├── api/
    │   ├── deployment.yaml
    │   ├── service.yaml
    │   ├── serviceaccount.yaml
    │   ├── rbac.yaml
    │   ├── pdb.yaml
    │   └── hpa.yaml
    ├── web/
    │   ├── deployment.yaml
    │   ├── service.yaml
    │   ├── serviceaccount.yaml
    │   ├── configmap-nginx.yaml
    │   ├── pdb.yaml
    │   └── hpa.yaml
    └── github-adapter/
        ├── deployment.yaml
        ├── service.yaml
        ├── serviceaccount.yaml
        ├── pdb.yaml
        └── hpa.yaml
```

## References

- Grafana-operator Helm chart: `~/go/src/github.com/NissesSenap/grafana-operator/deploy/helm/grafana-operator`
- Grafana-operator Makefile (CRD sync): `~/go/src/github.com/NissesSenap/grafana-operator/Makefile:69-70`
- Loki global image pattern: `~/go/src/github.com/NissesSenap/loki/production/helm/loki/templates/_helpers.tpl:161-170`
- Shepherd CLI flags: `cmd/shepherd/main.go:40-80`
- Shepherd operator config: `config/manager/manager.yaml`
- Shepherd API config: `config/api/deployment.yaml`
- Shepherd web config: `config/web/deployment.yaml`
- helm-docs: https://github.com/norwoodj/helm-docs
