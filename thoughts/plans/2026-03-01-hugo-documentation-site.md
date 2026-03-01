# Hugo Documentation Site Implementation Plan

## Overview

Set up a Hugo documentation site using the Hextra theme, deploy it to GitHub Pages via GitHub Actions, add `docs-*` Makefile targets, and write full content for all 11 documentation pages. The site covers quickstart, architecture, GitHub App setup, deployment, custom runners, API reference (Swagger UI), contributing, and troubleshooting.

## Current State Analysis

- No `docs/` directory exists — greenfield
- No Hugo config or GitHub Pages workflow
- Research completed in `thoughts/research/2026-03-01-documentation-site-planning.md` with detailed content outlines for all pages
- Makefile uses `web-*` prefix convention for frontend targets — we follow the same pattern with `docs-*`

### Key Discoveries:
- Central Makefile convention: all targets in root Makefile with category prefix (`Makefile:87-128` for `web-*` targets)
- GitHub Actions workflows live in `.github/workflows/` with descriptive names (`go-ci.yml`, `web-ci.yml`, `e2e.yml`, `release.yaml`)
- OpenAPI spec at `api/openapi.yaml` — will be copied to `docs/static/` for Swagger UI embedding

## Desired End State

- `docs/` directory with a working Hugo site using Hextra theme
- 11 fully-written documentation pages across 4 sections
- Swagger UI shortcode rendering `api/openapi.yaml` on the API reference page
- `make docs-serve`, `make docs-build`, `make docs-sync-openapi` targets in root Makefile
- GitHub Actions workflow deploying to `https://nissessenap.github.io/shepherd/` on pushes to `main` that touch `docs/**`
- Local preview works via `make docs-serve` at `http://localhost:1313/shepherd/`

### Verification:
- `make docs-build` completes without errors
- `make docs-serve` serves the site locally with all pages navigable
- All 11 pages render correctly with content
- Swagger UI loads and renders the OpenAPI spec on the API reference page
- GitHub Actions workflow file is valid YAML

## What We're NOT Doing

- Not implementing the GitHub App manifest flow as a code feature (docs-only)
- Not auto-generating API reference from OpenAPI (using Swagger UI embed instead)
- Not adding versioned docs (single version for now)
- Not adding search configuration beyond Hextra's built-in FlexSearch
- Not hosting a `/setup` endpoint for the manifest flow (docs-only, no code changes)

## Implementation Approach

4 phases, each self-contained and testable:

1. **Scaffold**: Hugo site init, Hextra theme, config, Makefile targets, GitHub Actions workflow, landing page
2. **Getting Started + Architecture**: quickstart, architecture overview, GitHub Apps explained
3. **Setup + Configuration**: GitHub App setup guide, deployment guide, configuration reference
4. **Extending + Supporting**: custom runners guide, API reference with Swagger UI, contributing, troubleshooting

---

## Phase 1: Hugo Scaffold & Infrastructure

### Overview
Create the Hugo site structure, configure Hextra theme, add Makefile targets, and create the GitHub Actions deployment workflow.

### Changes Required:

#### 1. Initialize Hugo site
**Directory**: `docs/`

Create Hugo site with Hextra theme as a Hugo module.

**File**: `docs/hugo.yaml`
```yaml
baseURL: "https://nissessenap.github.io/shepherd/"
title: "Shepherd"
enableGitInfo: true

module:
  imports:
    - path: github.com/imfing/hextra

markup:
  goldmark:
    renderer:
      unsafe: true  # Required for Swagger UI shortcode

params:
  navbar:
    displayTitle: true
    displayLogo: false
  theme:
    default: system
    displayToggle: true
  page:
    width: normal
  editURL:
    base: "https://github.com/NissesSenap/shepherd/edit/main/docs/content"

menu:
  main:
    - name: Documentation
      pageRef: /docs
      weight: 1
    - name: GitHub
      url: "https://github.com/NissesSenap/shepherd"
      weight: 5
      params:
        icon: github
```

**File**: `docs/go.mod`
Initialize with: `cd docs && hugo mod init github.com/NissesSenap/shepherd/docs`
Then: `cd docs && hugo mod get github.com/imfing/hextra`

#### 2. Create landing page
**File**: `docs/content/_index.md`

Hextra landing page with hero section, feature cards linking to docs sections.

#### 3. Create docs section root
**File**: `docs/content/docs/_index.md`

Root of the `/docs/` section — brief intro and navigation to sub-sections.

#### 4. Create section index pages (stubs for now, content in later phases)

Create `_index.md` files for each sub-section:
- `docs/content/docs/getting-started/_index.md`
- `docs/content/docs/architecture/_index.md`
- `docs/content/docs/setup/_index.md`
- `docs/content/docs/extending/_index.md`

These set the section title, weight (ordering), and sidebar configuration.

#### 5. Create Swagger UI shortcode
**File**: `docs/layouts/shortcodes/swagger.html`

```html
<div id="swagger-ui"></div>
<link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css">
<script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>
SwaggerUIBundle({
  url: "{{ .Get "src" }}",
  dom_id: '#swagger-ui',
  presets: [SwaggerUIBundle.presets.apis],
  layout: "BaseLayout"
});
</script>
```

#### 6. Add Makefile targets
**File**: `Makefile` — add new `##@ Documentation` section after the Frontend section (after line 128).

```makefile
##@ Documentation

.PHONY: docs-serve
docs-serve: ## Start Hugo dev server for documentation.
	cd docs && hugo server --buildDrafts --disableFastRender

.PHONY: docs-build
docs-build: ## Build documentation site for production.
	cd docs && hugo build --gc --minify

.PHONY: docs-sync-openapi
docs-sync-openapi: ## Copy OpenAPI spec to docs static directory.
	@mkdir -p docs/static
	cp api/openapi.yaml docs/static/openapi.yaml
	@echo "Synced api/openapi.yaml → docs/static/openapi.yaml"
```

#### 7. Create GitHub Actions workflow
**File**: `.github/workflows/hugo.yaml`

Triggers on pushes to `main` that touch `docs/**` or the workflow file itself. Uses `actions/configure-pages`, installs Hugo extended, builds with `--baseURL` from Pages config, deploys via `actions/deploy-pages`.

```yaml
name: Deploy docs

on:
  push:
    branches:
      - main
    paths:
      - 'docs/**'
      - '.github/workflows/hugo.yaml'
  workflow_dispatch:

permissions:
  contents: read
  pages: write
  id-token: write

concurrency:
  group: pages
  cancel-in-progress: false

jobs:
  build:
    runs-on: ubuntu-latest
    env:
      HUGO_VERSION: 0.147.6
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Setup Go
        uses: actions/setup-go@v5
        with:
          go-version-file: docs/go.mod
          cache: false

      - name: Setup Pages
        id: pages
        uses: actions/configure-pages@v5

      - name: Install Hugo
        run: |
          curl -sLO "https://github.com/gohugoio/hugo/releases/download/v${HUGO_VERSION}/hugo_extended_${HUGO_VERSION}_linux-amd64.tar.gz"
          mkdir -p "${HOME}/.local/hugo"
          tar -C "${HOME}/.local/hugo" -xf "hugo_extended_${HUGO_VERSION}_linux-amd64.tar.gz"
          echo "${HOME}/.local/hugo" >> "${GITHUB_PATH}"

      - name: Sync OpenAPI spec
        run: make docs-sync-openapi

      - name: Build
        working-directory: docs
        run: |
          hugo build \
            --gc \
            --minify \
            --baseURL "${{ steps.pages.outputs.base_url }}/"

      - name: Upload artifact
        uses: actions/upload-pages-artifact@v4
        with:
          path: docs/public

  deploy:
    environment:
      name: github-pages
      url: ${{ steps.deployment.outputs.page_url }}
    runs-on: ubuntu-latest
    needs: build
    steps:
      - name: Deploy to GitHub Pages
        id: deployment
        uses: actions/deploy-pages@v4
```

#### 8. Add docs build output to .gitignore
**File**: `.gitignore` — add `docs/public/` and `docs/resources/` (Hugo build artifacts), and `docs/static/openapi.yaml` (generated file).

### Success Criteria:

#### Automated Verification:
- [x] `cd docs && hugo mod get` succeeds (theme resolves)
- [x] `make docs-build` completes without errors
- [x] `make docs-sync-openapi` copies the file successfully
- [x] `make help` shows the new `docs-*` targets
- [x] `.github/workflows/hugo.yaml` is valid YAML

#### Manual Verification:
- [ ] `make docs-serve` shows the site at `http://localhost:1313/shepherd/`
- [ ] Landing page renders with Hextra theme
- [ ] Sidebar navigation shows all sections
- [ ] Dark/light mode toggle works

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation from the human that the site renders correctly before proceeding to content phases.

---

## Phase 2: Getting Started + Architecture Content

### Overview
Write full content for the quickstart guide, architecture overview, and GitHub Apps explained pages.

### Changes Required:

#### 1. Quickstart page
**File**: `docs/content/docs/getting-started/quickstart.md`

Full content covering two parts:

**Part 1 — Local-only testing (no GitHub App required):**
- Prerequisites: Kind, kubectl, ko, Docker, Go 1.25+, Node.js 22+
- Create Kind cluster: `make kind-create`
- Build and load images: `make ko-build-kind`
- Install dependencies: `make install-agent-sandbox && make install`
- Deploy test overlay: `make deploy-test`
- Verify: `kubectl get pods -n shepherd-system`
- Access web UI at `localhost:30081`, API at `localhost:30080`
- Create a test task via curl against `POST /api/v1/tasks`
- Frontend dev mode: `make web-dev`
- Note about test overlay (no GitHub App required, token endpoint returns 503)

**Part 2 — Connecting to GitHub (ngrok / smee):**

After the local-only section, a "Next: Connect to GitHub" section walks through exposing the local cluster to receive real GitHub webhooks.

**Option A: ngrok (recommended for full-stack)**
- Install ngrok, authenticate with free account
- `ngrok http 8082` to tunnel to the GitHub adapter
- Note the `https://XXXX.ngrok-free.app` URL
- Use this as the webhook URL when creating the GitHub App (link to GitHub App Setup page)
- Multi-port config (`ngrok.yml`) for exposing both adapter and API
- Gotchas: HMAC signatures pass through unchanged, browser interstitial doesn't affect webhooks, inspection UI at `http://127.0.0.1:4040`
- Free tier caveat: random subdomain changes on restart

**Option B: smee.io (webhook-only, simpler)**
- `npm install -g smee-client`
- Create a channel at https://smee.io
- `smee --url https://smee.io/YOUR_CHANNEL_ID --path /webhook --port 8082`
- Trade-off: only relays webhooks, can't expose API or web UI
- Advantage: permanent channel URL (no restart problem)

**Full end-to-end flow:**
1. Start ngrok/smee
2. Create GitHub Apps using manifests (link to GitHub App Setup page)
3. Store credentials as K8s secrets
4. Redeploy with GitHub App configuration (not the test overlay)
5. Install apps on target repo
6. Comment `@shepherd do something` on an issue
7. Watch the task appear in the web UI

Source: Research doc lines 45-56, 652-727.

#### 2. Architecture overview page
**File**: `docs/content/docs/architecture/overview.md`

Full content covering:
- Component diagram: 4 deployments (operator, API server, GitHub adapter, web frontend) + ephemeral runner sandboxes
- Two-port API architecture: public `:8080` and internal `:8081` (NetworkPolicy-protected)
- Full 10-step task lifecycle flow (GitHub webhook → adapter → API → CRD → operator → sandbox → runner → status → callback → GitHub comment)
- CRD model: AgentTask spec/status, conditions (Succeeded, Notified), terminal states
- Sandbox lifecycle: SandboxClaim → SandboxTemplate → Pod
- EventHub: in-memory pub/sub with 1000-event ring buffer, WebSocket fan-out
- Status watcher: backup callback mechanism

Source: Research doc lines 62-81.

#### 3. GitHub Apps explained page
**File**: `docs/content/docs/architecture/github-apps.md`

Full content covering:
- Why two apps: separation of concerns
- Trigger App: permissions (Issues read/write), webhook events (issue_comment), authentication flow
- Runner App: permissions (Contents read/write, Pull Requests read/write), no webhooks, app-level transport, per-request installation tokens
- One-time token issuance with anti-replay
- Shared callback secret (HMAC-SHA256)
- Important gotcha: same env var name for different apps

Source: Research doc lines 84-98.

### Success Criteria:

#### Automated Verification:
- [x] `make docs-build` completes without errors
- [x] No broken internal links (Hugo reports link errors during build)

#### Manual Verification:
- [ ] Quickstart page is readable and steps are clear
- [ ] Architecture overview has clear component descriptions
- [ ] GitHub Apps page explains the two-app model clearly
- [ ] All pages appear in sidebar navigation

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation.

---

## Phase 3: Setup + Configuration Content

### Overview
Write full content for the GitHub App setup guide, deployment guide, and configuration reference.

### Changes Required:

#### 1. GitHub App setup page
**File**: `docs/content/docs/setup/github-app-setup.md`

Full content covering:
- Manifest flow overview (3-step: redirect → callback → exchange)
- Trigger App manifest JSON (with `issues: write`, `issue_comment` event)
- Runner App manifest JSON (with `contents: write`, `pull_requests: write`)
- Registration URLs (personal and org)
- What the exchange returns (id, pem, webhook_secret)
- Manual setup alternative (step-by-step)
- K8s secret creation commands
- Installation on target repos/org

Source: Research doc lines 102-148, 352-388.

#### 2. Deployment guide
**File**: `docs/content/docs/setup/deployment.md`

Full content covering:
- Prerequisites: K8s cluster, cert-manager (optional), agent-sandbox operator
- Kustomize structure: `config/default/` composes CRD + RBAC + operator + API + web
- Installing CRDs: `make install`
- Installing agent-sandbox: `make install-agent-sandbox`
- Deploying Shepherd: `make deploy` with image configuration
- Environment variables reference for all 3 components (API, operator, adapter)
- RBAC requirements
- SandboxTemplate creation example
- Frontend deployment (nginx proxy)
- Optional: Prometheus ServiceMonitor

Source: Research doc lines 149-165.

#### 3. Configuration reference
**File**: `docs/content/docs/setup/configuration.md`

Full content covering:
- CLI flags and env vars table for each subcommand (`api`, `operator`, `github`)
- AgentTask CRD spec reference: all fields, types, validation, defaults
- SandboxTemplate reference
- RunnerSpec options (`sandboxTemplateName`, `timeout`, `serviceAccountName`, `resources`)
- Callback configuration (HMAC secret, URL requirements, signature format)
- Frontend configuration (`VITE_API_URL`, nginx proxy)

Source: Research doc lines 167-177.

### Success Criteria:

#### Automated Verification:
- [x] `make docs-build` completes without errors

#### Manual Verification:
- [ ] GitHub App setup guide has correct manifest JSON
- [ ] Deployment guide has accurate `make` commands
- [ ] Configuration reference covers all env vars

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation.

---

## Phase 4: Extending + Supporting Pages

### Overview
Write custom runners guide with Python/Node.js examples, API reference with Swagger UI, contributing guide, and troubleshooting page.

### Changes Required:

#### 1. Custom runners guide
**File**: `docs/content/docs/extending/custom-runners.md`

Full content covering:
- Runner protocol (5-step contract):
  1. Expose `POST /task` on port 8888
  2. Fetch task data via `GET /api/v1/tasks/{taskID}/data`
  3. Fetch GitHub token (one-time) via `GET /api/v1/tasks/{taskID}/token`
  4. Stream events (optional) via `POST /api/v1/tasks/{taskID}/events`
  5. Report completion via `POST /api/v1/tasks/{taskID}/status`
- Complete Python example (Flask-based runner)
- Complete Node.js example (Express-based runner)
- SandboxTemplate YAML for custom runners
- Event streaming protocol: sequence numbers, event types
- Key constraints: one-time token (409), terminal task data (410), timeout handling

Source: Research doc lines 182-292.

#### 2. API reference with Swagger UI
**File**: `docs/content/docs/extending/api-reference.md`

Content:
- Brief intro explaining the two-port architecture (public 8080, internal 8081)
- Swagger UI embed via shortcode: `{{</* swagger src="/openapi.yaml" */>}}`
- Additional notes on WebSocket protocol (`?after=N` reconnection)
- Error codes table (400, 404, 409, 410, 413, 415, 502, 503)
- Note that the internal endpoints are only accessible from within the cluster

**Dependency**: `make docs-sync-openapi` must run before `make docs-build` to copy the spec.
Update the `docs-build` target to depend on `docs-sync-openapi`.

#### 3. Contributing guide
**File**: `docs/content/docs/contributing.md`

Full content covering:
- `make build` / `make test` / `make lint-fix` workflow
- Frontend dev: `make web-dev` / `make web-test` / `make web-check` / `make web-lint-fix`
- CRD changes: edit `api/v1alpha1/` → `make manifests generate`
- API changes: edit `api/openapi.yaml` → `make web-gen-types`
- E2E testing: `make test-e2e-interactive`
- Code conventions: testify, httptest, table-driven tests, Svelte 5 runes only

Source: Research doc lines 308-315.

#### 4. Troubleshooting page
**File**: `docs/content/docs/troubleshooting.md`

Full content covering:
- `SHEPHERD_GITHUB_APP_ID` env var confusion (same name, different apps)
- Token endpoint 503: GitHub App not configured
- Token endpoint 409: token already issued
- Task stuck in Pending: SandboxClaim not becoming Ready
- Callback not delivered: check ConditionNotified on AgentTask
- golangci-lint failures: don't scaffold unused functions
- `go mod tidy` removes unused packages

Source: Research doc lines 317-326.

#### 5. Update Makefile docs-build target
**File**: `Makefile` — make `docs-build` depend on `docs-sync-openapi`:

```makefile
.PHONY: docs-build
docs-build: docs-sync-openapi ## Build documentation site for production.
	cd docs && hugo build --gc --minify
```

### Success Criteria:

#### Automated Verification:
- [ ] `make docs-build` completes without errors (includes OpenAPI sync)
- [ ] `docs/static/openapi.yaml` is generated

#### Manual Verification:
- [ ] Custom runners page has working Python and Node.js examples
- [ ] Swagger UI loads and renders the OpenAPI spec interactively
- [ ] Contributing page has accurate make commands
- [ ] Troubleshooting page covers common issues
- [ ] Full site navigation works — all 11 pages accessible from sidebar

**Implementation Note**: After completing this phase, the documentation site is complete. Enable GitHub Pages (Settings → Pages → Source: GitHub Actions) and push to `main` to deploy.

---

## Testing Strategy

### Automated (per phase):
- `make docs-build` — Hugo build succeeds
- `make docs-sync-openapi` — OpenAPI spec copied

### Manual (after all phases):
1. Run `make docs-serve`, navigate all pages
2. Verify Swagger UI loads the spec
3. Check sidebar navigation order
4. Test dark/light mode toggle
5. Verify "Edit this page" links point to correct GitHub URLs
6. Test on mobile viewport

## File Summary

New files to create:
```
docs/
├── hugo.yaml
├── go.mod                              (generated by hugo mod init)
├── go.sum                              (generated by hugo mod get)
├── layouts/
│   └── shortcodes/
│       └── swagger.html
├── content/
│   ├── _index.md                       # Landing page
│   └── docs/
│       ├── _index.md                   # Docs root
│       ├── getting-started/
│       │   ├── _index.md               # Section index
│       │   └── quickstart.md
│       ├── architecture/
│       │   ├── _index.md               # Section index
│       │   ├── overview.md
│       │   └── github-apps.md
│       ├── setup/
│       │   ├── _index.md               # Section index
│       │   ├── github-app-setup.md
│       │   ├── deployment.md
│       │   └── configuration.md
│       ├── extending/
│       │   ├── _index.md               # Section index
│       │   ├── custom-runners.md
│       │   └── api-reference.md
│       ├── contributing.md
│       └── troubleshooting.md
└── static/
    └── openapi.yaml                    (generated, gitignored)

.github/workflows/hugo.yaml            # GitHub Pages deployment
```

Modified files:
- `Makefile` — add `##@ Documentation` section with `docs-serve`, `docs-build`, `docs-sync-openapi`
- `.gitignore` — add `docs/public/`, `docs/resources/`, `docs/static/openapi.yaml`

## References

- Research: `thoughts/research/2026-03-01-documentation-site-planning.md`
- OpenAPI spec: `api/openapi.yaml`
- Hextra theme: `github.com/imfing/hextra`
- Hugo modules: requires Go (already in the project)
