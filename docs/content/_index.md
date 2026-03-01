---
title: Shepherd
toc: false
---

{{< hextra/hero-badge link="https://github.com/NissesSenap/shepherd" >}}
  <span>GitHub</span>
  {{< icon name="arrow-circle-right" attributes="height=14" >}}
{{< /hextra/hero-badge >}}

<div class="hx-mt-6 hx-mb-6">
{{< hextra/hero-headline >}}
  Background Coding Agent&nbsp;<br class="sm:hx-block hx-hidden" />Orchestrator on Kubernetes
{{< /hextra/hero-headline >}}
</div>

<div class="hx-mb-12">
{{< hextra/hero-subtitle >}}
  Trigger AI coding agents from GitHub issue comments.&nbsp;<br class="sm:hx-block hx-hidden" />Shepherd orchestrates sandboxed runners that open PRs back to your repo.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx-mb-6">
{{< hextra/hero-button text="Get Started" link="docs/getting-started/quickstart" >}}
</div>

<div class="hx-mt-6"></div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="GitHub Native"
    subtitle="Comment @shepherd on any issue to trigger a task. Results come back as pull requests."
    style="background: radial-gradient(ellipse at 50% 80%,rgba(72,120,198,0.15),hsla(0,0%,100%,0));"
  >}}
  {{< hextra/feature-card
    title="Kubernetes Orchestration"
    subtitle="Runs on any K8s cluster. The operator manages sandboxed pods for each coding task."
    style="background: radial-gradient(ellipse at 50% 80%,rgba(72,198,120,0.15),hsla(0,0%,100%,0));"
  >}}
  {{< hextra/feature-card
    title="Pluggable Runners"
    subtitle="Bring your own runner image — any container that speaks the runner protocol works."
    style="background: radial-gradient(ellipse at 50% 80%,rgba(198,120,72,0.15),hsla(0,0%,100%,0));"
  >}}
{{< /hextra/feature-grid >}}
