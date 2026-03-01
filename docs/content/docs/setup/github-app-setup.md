---
title: GitHub App Setup
weight: 1
---

Shepherd requires two GitHub Apps: a **Trigger App** for the adapter (webhooks and comments) and a **Runner App** for the API server (repository access tokens). This guide walks through creating both apps using the manifest flow or manual setup.

If you haven't read about why Shepherd uses two apps, see [GitHub Apps Explained](../../architecture/github-apps/).

## Manifest Flow Overview

GitHub's [App Manifest flow](https://docs.github.com/en/apps/sharing-github-apps/registering-a-github-app-from-a-manifest) lets you create a GitHub App from a JSON definition in three steps:

1. **Redirect** — send the user to GitHub with the manifest JSON.
2. **Callback** — GitHub redirects back with a temporary `code` parameter.
3. **Exchange** — `POST /app-manifests/{code}/conversions` returns the app credentials.

All three steps must complete within **1 hour**.

{{< callout type="info" >}}
The manifest flow requires submitting a **form POST** to GitHub — you cannot simply visit a URL and paste JSON. Shepherd ships a small HTML helper page to make this easy, or you can create the apps manually (see [Manual Setup](#manual-setup-alternative) below).
{{< /callout >}}

## Step 1: Create the Trigger App

The Trigger App receives webhooks and posts comments on issues.

### Manifest

```json
{
  "name": "Shepherd Trigger",
  "url": "https://github.com/NissesSenap/shepherd",
  "hook_attributes": {
    "url": "https://<your-adapter-host>/webhook",
    "active": true
  },
  "public": false,
  "default_permissions": {
    "issues": "write"
  },
  "default_events": [
    "issue_comment"
  ]
}
```

Replace `<your-adapter-host>` with your adapter's public URL (e.g., your ngrok URL from the [Quickstart](../../getting-started/quickstart/#option-a-ngrok-recommended)).

### Register

The manifest must be submitted as a form POST to GitHub. Save the following as an HTML file (e.g., `register-trigger.html`), edit the `webhookUrl` value, then open it in your browser:

```html
<html><body>
<form action="https://github.com/settings/apps/new?state=trigger" method="post">
  <input type="hidden" name="manifest" id="manifest">
  <input type="submit" value="Create Shepherd Trigger App">
</form>
<script>
  // Change this to your adapter's public URL
  const webhookUrl = "https://XXXX.ngrok-free.app/webhook";

  document.getElementById("manifest").value = JSON.stringify({
    name: "Shepherd Trigger",
    url: "https://github.com/NissesSenap/shepherd",
    hook_attributes: { url: webhookUrl, active: true },
    public: false,
    default_permissions: { issues: "write" },
    default_events: ["issue_comment"]
  });
</script>
</body></html>
```

{{< callout type="tip" >}}
For an **organization app**, change the form action to `https://github.com/organizations/<ORG>/settings/apps/new?state=trigger`.
{{< /callout >}}

1. Open the HTML file in your browser and click **Create Shepherd Trigger App**.
2. GitHub shows the pre-filled app creation form — review and click **Create GitHub App**.
3. GitHub redirects you back. The response URL contains a `code` parameter. Exchange it:

   ```bash
   curl -s -X POST https://api.github.com/app-manifests/<CODE>/conversions | jq '{id, pem, webhook_secret}'
   ```

   This returns:

   | Field | Use |
   |-------|-----|
   | `id` | `SHEPHERD_GITHUB_APP_ID` for the adapter |
   | `pem` | Private key — save to a file |
   | `webhook_secret` | `SHEPHERD_GITHUB_WEBHOOK_SECRET` |

4. **Save all three values** — you'll need them to create Kubernetes secrets.

## Step 2: Create the Runner App

The Runner App generates repository-scoped tokens for runners to clone repos and create PRs. It has **no webhook configuration**.

### Manifest

```json
{
  "name": "Shepherd Runner",
  "url": "https://github.com/NissesSenap/shepherd",
  "public": false,
  "default_permissions": {
    "contents": "write",
    "pull_requests": "write"
  },
  "default_events": []
}
```

### Register

Save this as `register-runner.html` and open it in your browser:

```html
<html><body>
<form action="https://github.com/settings/apps/new?state=runner" method="post">
  <input type="hidden" name="manifest" id="manifest">
  <input type="submit" value="Create Shepherd Runner App">
</form>
<script>
  document.getElementById("manifest").value = JSON.stringify({
    name: "Shepherd Runner",
    url: "https://github.com/NissesSenap/shepherd",
    public: false,
    default_permissions: { contents: "write", pull_requests: "write" },
    default_events: []
  });
</script>
</body></html>
```

1. Click **Create Shepherd Runner App**, review, and confirm.
2. Exchange the code: `curl -s -X POST https://api.github.com/app-manifests/<CODE>/conversions | jq '{id, pem}'`
3. Save the returned `id` and `pem`. The Runner App doesn't have a `webhook_secret`.

## Step 3: Install Both Apps

After creating each app, you need to install it on the repositories (or organization) where you want Shepherd to operate:

1. Go to `https://github.com/settings/apps/<app-name>/installations` (or the equivalent org URL).
2. Click **Install** and select the target repository or organization.
3. Note the **installation ID** from the URL after installation (e.g., `https://github.com/settings/installations/<INSTALLATION_ID>`).

You need the installation ID for each app.

## Step 4: Create Kubernetes Secrets

Create separate secrets for each app:

```bash
# Trigger App secret (used by the GitHub adapter)
kubectl create secret generic shepherd-trigger-app \
  --namespace shepherd-system \
  --from-literal=app-id=<TRIGGER_APP_ID> \
  --from-literal=installation-id=<TRIGGER_INSTALLATION_ID> \
  --from-file=private-key=<TRIGGER_PRIVATE_KEY_FILE> \
  --from-literal=webhook-secret=<TRIGGER_WEBHOOK_SECRET>

# Runner App secret (used by the API server)
kubectl create secret generic shepherd-github-app \
  --namespace shepherd-system \
  --from-literal=app-id=<RUNNER_APP_ID> \
  --from-literal=installation-id=<RUNNER_INSTALLATION_ID> \
  --from-file=private-key=<RUNNER_PRIVATE_KEY_FILE>
```

The API server's deployment mounts `shepherd-github-app` at `/etc/shepherd/github-app-key` and reads the `app-id` and `installation-id` from environment variables sourced from the secret.

## Manual Setup Alternative

If you prefer not to use the manifest flow, you can create each app manually:

### Trigger App (Manual)

1. Go to **Settings > Developer settings > GitHub Apps > New GitHub App**.
2. Set the following:
   - **App name**: `Shepherd Trigger` (or your preference)
   - **Homepage URL**: your project URL
   - **Webhook URL**: `https://<your-adapter-host>/webhook`
   - **Webhook secret**: generate a strong random secret
3. Under **Permissions**:
   - **Repository permissions > Issues**: Read & Write
4. Under **Subscribe to events**:
   - Check **Issue comment**
5. Click **Create GitHub App**.
6. On the app page, click **Generate a private key** and save the `.pem` file.

### Runner App (Manual)

1. Create another new GitHub App.
2. Set the following:
   - **App name**: `Shepherd Runner`
   - **Homepage URL**: your project URL
   - **Webhook**: **uncheck** Active (no webhooks needed)
3. Under **Permissions**:
   - **Repository permissions > Contents**: Read & Write
   - **Repository permissions > Pull requests**: Read & Write
4. Click **Create GitHub App**.
5. Generate and save a private key.

{{< callout type="warning" >}}
**Same env var, different apps**: Both the adapter and API server use `SHEPHERD_GITHUB_APP_ID` as the environment variable name, but they refer to **different GitHub Apps**. Make sure you configure each component with its own app's credentials.
{{< /callout >}}

## Manifest Parameter Reference

For advanced customization, here are all supported manifest fields:

| Parameter | Type | Required | Notes |
|-----------|------|----------|-------|
| `name` | string | No | App name (editable by user during creation) |
| `url` | string | **Yes** | App homepage URL |
| `hook_attributes` | object | No | `{url, active}` — webhook endpoint |
| `redirect_url` | string | No | Where GitHub sends user after registration |
| `description` | string | No | App description |
| `public` | boolean | No | Public or private app |
| `default_events` | array | No | Webhook event subscriptions |
| `default_permissions` | object | No | Permission name to access level |

## Next Steps

- [Deployment Guide](../deployment/) — deploy Shepherd to your cluster with the secrets you created
- [Configuration Reference](../configuration/) — all environment variables and CRD fields
- [Troubleshooting](../../troubleshooting/) — common issues with GitHub App configuration
