# Issue #38: Repo Filter Returns 500 — Fix with TDD

## Overview

Fix the repo filter dropdown that causes a 500 Internal Server Error by sending full GitHub URLs as Kubernetes label selector values. Apply Option C: fix the frontend to send label-compatible values, and add backend normalization + validation as a safety net.

## Current State Analysis

**The bug**: FilterBar sends `https://github.com/test-org/test-repo` as `?repo=...`, but `shepherd.io/repo` labels store `test-org-test-repo` (slashes replaced with dashes). Kubernetes rejects the URL as an invalid label value → 500.

### Key Discoveries:
- `pkg/adapters/github/webhook.go:177` — `repoLabel := strings.ReplaceAll(repoFullName, "/", "-")` stores dash-separated form
- `web/src/lib/components/FilterBar.svelte:34` — collects full URLs: `set.add(task.repo.url)`
- `web/src/lib/components/FilterBar.svelte:95` — option value is full URL: `<option value={repo}>`
- `pkg/api/handler_tasks.go:249-250` — uses value directly: `labelSelector["shepherd.io/repo"] = repo`
- `pkg/api/handler_tasks.go:42` — `labelValueRegex` already exists for validation
- Existing tests in `handler_tasks_test.go:596-611` use `"org-repo1"` form and `?repo=org-repo1` — confirms the expected label format

## Desired End State

1. **Frontend**: The repo filter dropdown sends label-compatible values (e.g., `org-repo`) instead of full URLs
2. **Backend**: The `listTasks` handler validates all label filter params and returns 400 (not 500) for invalid values. For the `repo` param specifically, it normalizes URLs to label format before querying
3. **All existing tests continue to pass** — the normalization is backward-compatible

### Verification:
- `make test` passes (includes new Go tests)
- `make lint-fix` clean
- `make web-test` passes (includes new frontend tests)
- `make web-lint-fix` clean
- `make web-check` clean
- Manual: selecting a repo from the filter dropdown returns filtered results without errors

## What We're NOT Doing

- Not changing how labels are stored during task creation (the adapter already stores them correctly)
- Not modifying the OpenAPI spec (the `repo` param description already says "Filter by shepherd.io/repo label")
- Not adding normalization for `issue` or `fleet` params (they don't have this URL problem), but we will add validation to return 400 instead of 500
- Not changing the `createTask` endpoint or CRD types

## Implementation Approach

Option C with TDD: Fix both ends. Write failing tests first, then implement the minimal code to make them pass.

---

## Phase 1: Backend — `normalizeRepoFilter` Function (TDD)

### Overview
Add a pure function that normalizes repo filter values from URLs to K8s label format. Write tests first.

### Step 1.1: Write Failing Tests

**File**: `pkg/api/handler_tasks_test.go`

Add table-driven tests for `normalizeRepoFilter`:

```go
func TestNormalizeRepoFilter(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:  "already valid label (dash form)",
			input: "org-repo",
			want:  "org-repo",
		},
		{
			name:  "full GitHub HTTPS URL",
			input: "https://github.com/test-org/test-repo",
			want:  "test-org-test-repo",
		},
		{
			name:  "full URL with .git suffix",
			input: "https://github.com/test-org/test-repo.git",
			want:  "test-org-test-repo",
		},
		{
			name:  "slash form (org/repo)",
			input: "test-org/test-repo",
			want:  "test-org-test-repo",
		},
		{
			name:  "non-GitHub URL",
			input: "https://gitlab.com/org/repo",
			want:  "org-repo",
		},
		{
			name:  "URL with deep path",
			input: "https://github.com/org/sub/repo",
			want:  "org-sub-repo",
		},
		{
			name:    "invalid chars after normalization",
			input:   "$$invalid$$",
			wantErr: true,
		},
		{
			name:    "URL with empty path",
			input:   "https://github.com/",
			wantErr: true,
		},
		{
			name:    "value exceeds 63 chars after normalization",
			input:   "https://github.com/" + strings.Repeat("a", 64),
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeRepoFilter(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
```

Run: `make test` → tests fail (function doesn't exist)

### Step 1.2: Implement `normalizeRepoFilter`

**File**: `pkg/api/handler_tasks.go`

Add function after `validateLabelValue`:

```go
// normalizeRepoFilter converts a repo filter value to a valid Kubernetes label value.
// It handles full URLs (https://github.com/org/repo), slash forms (org/repo),
// and already-valid label values (org-repo).
func normalizeRepoFilter(value string) (string, error) {
	if strings.HasPrefix(value, "https://") || strings.HasPrefix(value, "http://") {
		u, err := url.Parse(value)
		if err != nil {
			return "", fmt.Errorf("invalid repo filter URL: %w", err)
		}
		value = strings.TrimPrefix(u.Path, "/")
		value = strings.TrimSuffix(value, ".git")
	}
	value = strings.ReplaceAll(value, "/", "-")
	if value == "" {
		return "", fmt.Errorf("repo filter is empty after normalization")
	}
	if err := validateLabelValue(value); err != nil {
		return "", fmt.Errorf("repo filter is not a valid label value: %w", err)
	}
	return value, nil
}
```

Note: `strings` and `url` are already imported.

Run: `make test` → `TestNormalizeRepoFilter` passes

### Success Criteria:

#### Automated Verification:
- [x] `make test` — `TestNormalizeRepoFilter` passes
- [x] `make lint-fix` — clean

---

## Phase 2: Backend — Wire Normalization into `listTasks` and Add Validation (TDD)

### Overview
Update `listTasks` to normalize the `repo` param and validate all filter params. Write integration tests first.

### Step 2.1: Write Failing Tests

**File**: `pkg/api/handler_tasks_test.go`

```go
func TestListTasks_RepoFilterNormalizesFullURL(t *testing.T) {
	task := newTask("task-aaa", map[string]string{"shepherd.io/repo": "test-org-test-repo"}, nil)
	h := newTestHandler(task)
	router := testRouter(h)

	// Send full URL — handler should normalize to label form and match
	w := doGet(t, router, "/api/v1/tasks?repo="+url.QueryEscape("https://github.com/test-org/test-repo"))

	assert.Equal(t, http.StatusOK, w.Code)
	var tasks []TaskResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &tasks))
	assert.Len(t, tasks, 1)
	assert.Equal(t, "task-aaa", tasks[0].ID)
}

func TestListTasks_RepoFilterRejectsInvalidValue(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks?repo="+url.QueryEscape("$$invalid$$"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, "invalid repo filter")
}

func TestListTasks_InvalidIssueLabelValue(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks?issue="+url.QueryEscape("not/valid/label"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, "invalid issue filter")
}

func TestListTasks_InvalidFleetLabelValue(t *testing.T) {
	h := newTestHandler()
	router := testRouter(h)

	w := doGet(t, router, "/api/v1/tasks?fleet="+url.QueryEscape("not/valid/label"))

	assert.Equal(t, http.StatusBadRequest, w.Code)
	var errResp ErrorResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &errResp))
	assert.Contains(t, errResp.Error, "invalid fleet filter")
}
```

Run: `make test` → new tests fail (handler doesn't validate/normalize yet)

### Step 2.2: Update `listTasks`

**File**: `pkg/api/handler_tasks.go`

Replace the label selector building block (lines 247-260) with:

```go
	// Build label selector from query params
	labelSelector := map[string]string{}
	if repo := r.URL.Query().Get("repo"); repo != "" {
		normalized, err := normalizeRepoFilter(repo)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid repo filter", err.Error())
			return
		}
		labelSelector["shepherd.io/repo"] = normalized
	}
	if issue := r.URL.Query().Get("issue"); issue != "" {
		if err := validateLabelValue(issue); err != nil {
			writeError(w, http.StatusBadRequest, "invalid issue filter", err.Error())
			return
		}
		labelSelector["shepherd.io/issue"] = issue
	}
	if fleet := r.URL.Query().Get("fleet"); fleet != "" {
		if err := validateLabelValue(fleet); err != nil {
			writeError(w, http.StatusBadRequest, "invalid fleet filter", err.Error())
			return
		}
		labelSelector["shepherd.io/fleet"] = fleet
	}
```

Run: `make test` → all tests pass (including existing `TestListTasks_FilterByRepoLabel` which sends valid label values)

### Success Criteria:

#### Automated Verification:
- [ ] `make test` — all handler_tasks tests pass
- [ ] `make lint-fix` — clean
- [ ] Existing `TestListTasks_FilterByRepoLabel` still passes (backward compatible)
- [ ] `TestListTasks_RepoFilterNormalizesFullURL` passes
- [ ] `TestListTasks_RepoFilterRejectsInvalidValue` passes
- [ ] `TestListTasks_InvalidIssueLabelValue` passes
- [ ] `TestListTasks_InvalidFleetLabelValue` passes

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation before proceeding to the frontend phase.

---

## Phase 3: Frontend — `repoUrlToLabel` Utility (TDD)

### Overview
Add a utility function to convert repo URLs to K8s label-compatible values. Write tests first.

### Step 3.1: Write Failing Tests

**File**: `web/src/lib/filters.test.ts`

Add a new describe block:

```typescript
// ---------------------------------------------------------------------------
// repoUrlToLabel
// ---------------------------------------------------------------------------
describe("repoUrlToLabel", () => {
	it("converts full GitHub URL to label form", () => {
		expect(repoUrlToLabel("https://github.com/org/repo")).toBe("org-repo");
	});

	it("strips .git suffix from URL", () => {
		expect(repoUrlToLabel("https://github.com/org/repo.git")).toBe("org-repo");
	});

	it("handles non-GitHub URLs", () => {
		expect(repoUrlToLabel("https://gitlab.com/org/repo")).toBe("org-repo");
	});

	it("converts slash form to dash form", () => {
		expect(repoUrlToLabel("org/repo")).toBe("org-repo");
	});

	it("passes through already-valid label values", () => {
		expect(repoUrlToLabel("org-repo")).toBe("org-repo");
	});

	it("handles deep paths", () => {
		expect(repoUrlToLabel("https://github.com/org/sub/repo")).toBe(
			"org-sub-repo",
		);
	});
});
```

Also add the import: `import { repoUrlToLabel } from "./filters.js";`

Run: `make web-test` → tests fail (function doesn't exist)

### Step 3.2: Implement `repoUrlToLabel`

**File**: `web/src/lib/filters.ts`

Add function:

```typescript
/**
 * Convert a repo URL or path to a Kubernetes label-compatible value.
 * Strips URL scheme/host, removes trailing .git, and replaces slashes with dashes.
 *
 * Examples:
 *   "https://github.com/org/repo"     → "org-repo"
 *   "https://github.com/org/repo.git" → "org-repo"
 *   "org/repo"                        → "org-repo"
 *   "org-repo"                        → "org-repo"
 */
export function repoUrlToLabel(url: string): string {
	let value: string;
	try {
		const parsed = new URL(url);
		value = parsed.pathname.replace(/^\//, "").replace(/\.git$/, "");
	} catch {
		value = url;
	}
	return value.replace(/\//g, "-");
}
```

Run: `make web-test` → `repoUrlToLabel` tests pass

### Success Criteria:

#### Automated Verification:
- [ ] `make web-test` — all filter tests pass
- [ ] `make web-lint-fix` — clean
- [ ] `make web-check` — clean

---

## Phase 4: Frontend — Update FilterBar Component

### Overview
Update the FilterBar dropdown to use `repoUrlToLabel` for option values.

### Step 4.1: Update FilterBar

**File**: `web/src/lib/components/FilterBar.svelte`

1. Add import at the top of the script block:
```typescript
import { repoUrlToLabel } from "$lib/filters.js";
```

2. Change the `<option>` element (line 95) from:
```svelte
<option value={repo}>{repo.replace(/^https:\/\/github\.com\//, "")}</option>
```
to:
```svelte
<option value={repoUrlToLabel(repo)}>{repo.replace(/^https:\/\/github\.com\//, "")}</option>
```

This changes the value sent as the `repo` URL parameter from the full URL to the label-compatible form (e.g., `org-repo`), while keeping the human-readable display text unchanged.

### Success Criteria:

#### Automated Verification:
- [ ] `make web-test` — all tests pass
- [ ] `make web-lint-fix` — clean
- [ ] `make web-check` — type checking passes

#### Manual Verification:
- [ ] Open the task list with 2+ repos
- [ ] Select a repo from the filter dropdown
- [ ] Verify the API returns filtered results (no 500 error)
- [ ] Verify the URL params show the short form (e.g., `?repo=org-repo`)
- [ ] Verify selecting "All repos" clears the filter
- [ ] Verify the dropdown shows the correct selection after page reload

**Implementation Note**: After completing this phase and all automated verification passes, pause here for manual confirmation that the filter works correctly end-to-end.

---

## Testing Strategy

### Unit Tests (Go):
- `TestNormalizeRepoFilter` — table-driven: valid labels, full URLs, slash forms, .git suffix, deep paths, invalid inputs, empty-after-normalization, too-long values
- `TestListTasks_RepoFilterNormalizesFullURL` — integration test with handler + fake K8s client
- `TestListTasks_RepoFilterRejectsInvalidValue` — verifies 400 response
- `TestListTasks_InvalidIssueLabelValue` — verifies 400 for invalid issue param
- `TestListTasks_InvalidFleetLabelValue` — verifies 400 for invalid fleet param

### Unit Tests (TypeScript):
- `repoUrlToLabel` — full URLs, .git suffix, non-GitHub URLs, slash form, pass-through, deep paths

### Existing Tests (must continue to pass):
- `TestListTasks_FilterByRepoLabel` — sends valid `?repo=org-repo1` (backward compatible)
- `TestListTasks_CombinedFilters` — sends valid `?repo=org-repo&issue=42&active=true`
- `TestCreateTask_WithLabels` — label creation is unchanged
- All existing `filters.test.ts` tests

### Manual Testing Steps:
1. Start the API server and frontend dev server
2. Create tasks targeting different repos
3. Open the task list and select a repo from the filter dropdown
4. Verify filtered results appear correctly
5. Verify no 500 errors in the API logs
6. Test with browser back/forward buttons to ensure URL param handling works

## References

- Original issue: https://github.com/nissessenap/shepherd/issues/38
- `pkg/adapters/github/webhook.go:177` — where `shepherd.io/repo` label value is created
- `pkg/api/handler_tasks.go:249-250` — where repo filter is used as label selector
- `web/src/lib/components/FilterBar.svelte:95` — where option value is set to full URL
