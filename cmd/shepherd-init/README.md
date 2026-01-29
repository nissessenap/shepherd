# shepherd-init

Init container for Shepherd AgentTask Jobs. Generates GitHub App installation tokens and writes task files for the runner container.

## Purpose

The shepherd-init container runs as an init container in AgentTask Job pods. It performs two critical setup tasks:

1. Generates a short-lived GitHub App installation token scoped to the target repository
2. Writes task description and context files for the runner to consume

## Environment Variables

The init container reads the following environment variables:

### Required

- `TASK_DESCRIPTION`: The task description text (written to `/task/description.txt`)
- `REPO_URL`: The GitHub repository URL for token generation (must be `https://host/owner/repo` or `https://host/owner/repo.git`)
- `GITHUB_APP_ID`: GitHub App ID for authentication
- `GITHUB_INSTALLATION_ID`: GitHub App installation ID for the target organization/user
- `GITHUB_API_URL`: GitHub API base URL (e.g., `https://api.github.com`)

### Optional

- `TASK_CONTEXT`: Additional task context (written to `/task/context.txt` if present, otherwise writes empty file)
- `CONTEXT_ENCODING`: Encoding format for `TASK_CONTEXT` (supported values: `""` for no encoding, `"gzip"` for gzip-compressed+base64-encoded)

## Output Files

The init container writes the following files:

- `/task/description.txt`: Task description text (mode 0644)
- `/task/context.txt`: Task context text or empty file (mode 0644)
- `/creds/token`: GitHub App installation token (mode 0600)

## REPO_URL Validation

The `REPO_URL` must be a valid Git clone URL in one of these formats:

- `https://github.com/owner/repo`
- `https://github.com/owner/repo.git`
- `https://github.example.com/owner/repo` (GitHub Enterprise)

Web UI URLs are explicitly rejected. Examples of invalid URLs:

- `https://github.com/org/repo/tree/main` (branch view)
- `https://github.com/org/repo/issues/42` (issue page)
- `https://github.com/org/repo/blob/main/README.md` (file view)

This strict validation is intentional and critical for correct GitHub App token scoping. The GitHub App installation token API requires the repository owner and name to scope permissions. Web UI URLs contain extra path segments that would cause token generation to fail or produce incorrectly-scoped tokens.

If you receive a validation error about REPO_URL format, ensure you are using the repository's clone URL, not a browser URL.
