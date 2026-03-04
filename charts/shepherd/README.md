---
title: Helm Chart Values
weight: 4
---

# shepherd

![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.1.0](https://img.shields.io/badge/AppVersion-0.1.0-informational?style=flat-square)

Background coding agent orchestrator for Kubernetes

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

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| NissesSenap |  | <https://github.com/NissesSenap> |

## Source Code

* <https://github.com/NissesSenap/shepherd>

## Requirements

Kubernetes: `>=1.28.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| api.affinity | object | `{}` | Affinity rules for the API pods |
| api.annotations | object | `{}` | Annotations for the API deployment |
| api.githubApp.enabled | bool | `false` | Enable GitHub App integration for token generation |
| api.githubApp.existingSecret | string | `""` | Name of the existing Secret containing GitHub App credentials. Must contain keys: app-id, installation-id, private-key |
| api.hpa.enabled | bool | `false` | Enable HorizontalPodAutoscaler for the API |
| api.hpa.maxReplicas | int | `5` | Maximum number of replicas |
| api.hpa.metrics | list | `[{"resource":{"name":"cpu","target":{"averageUtilization":80,"type":"Utilization"}},"type":"Resource"}]` | Metrics for the HPA |
| api.hpa.minReplicas | int | `2` | Minimum number of replicas |
| api.image.pullPolicy | string | `"IfNotPresent"` | API image pull policy |
| api.image.registry | string | `"ghcr.io"` | API image registry |
| api.image.repository | string | `"nissessenap/shepherd"` | API image repository (same binary as operator) |
| api.image.tag | string | .Chart.AppVersion | API image tag (defaults to chart appVersion) |
| api.imagePullSecrets | list | `[]` | Image pull secrets for the API (overrides global) |
| api.nodeSelector | object | `{}` | Node selector for the API pods |
| api.pdb.enabled | bool | `false` | Enable PodDisruptionBudget for the API |
| api.pdb.maxUnavailable | string | not set | Maximum unavailable pods (mutually exclusive with minAvailable) |
| api.pdb.minAvailable | int | `1` | Minimum available pods (mutually exclusive with maxUnavailable) |
| api.podAnnotations | object | `{}` | Annotations for the API pods |
| api.podLabels | object | `{}` | Labels for the API pods |
| api.podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context for the API |
| api.rbac.create | bool | `true` | Whether to create RBAC resources for the API |
| api.replicas | int | `2` | Number of API server replicas |
| api.resources | object | `{"limits":{"cpu":"500m","memory":"128Mi"},"requests":{"cpu":"10m","memory":"64Mi"}}` | Resource requests and limits for the API |
| api.securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container security context for the API |
| api.service.annotations | object | `{}` | Annotations for the API service |
| api.service.internalPort | int | `8081` | Internal API port (for runner communication) |
| api.service.port | int | `8080` | Public API port |
| api.service.type | string | `"ClusterIP"` | API service type |
| api.serviceAccount.annotations | object | `{}` | Annotations to add to the API service account |
| api.serviceAccount.create | bool | `true` | Whether to create a service account for the API |
| api.serviceAccount.name | string | fullname-api | The name of the API service account |
| api.tolerations | list | `[]` | Tolerations for the API pods |
| crds.install | bool | `true` | Whether to install CRDs with the chart. CRDs are placed in templates/ (not crds/) so they are updated on helm upgrade. Protected with helm.sh/resource-policy: keep to prevent deletion on chart uninstall. |
| extraObjects | list | `[]` | Array of extra K8s objects to deploy (supports templating) |
| fullnameOverride | string | derived from release name + chart name | Overrides the fully qualified app name |
| githubAdapter.affinity | object | `{}` | Affinity rules for the GitHub adapter pods |
| githubAdapter.annotations | object | `{}` | Annotations for the GitHub adapter deployment |
| githubAdapter.callbackURL | string | `""` | Callback URL that the API server will call back to |
| githubAdapter.defaultSandboxTemplate | string | `"default"` | Default sandbox template name for new tasks |
| githubAdapter.enabled | bool | `false` | Enable the GitHub adapter component |
| githubAdapter.existingSecret | string | `""` | Name of the existing Secret containing GitHub App credentials. Must contain keys: webhook-secret, app-id, installation-id, private-key. Optionally: callback-secret. |
| githubAdapter.hpa.enabled | bool | `false` | Enable HorizontalPodAutoscaler for the GitHub adapter |
| githubAdapter.hpa.maxReplicas | int | `5` | Maximum number of replicas |
| githubAdapter.hpa.metrics | list | `[{"resource":{"name":"cpu","target":{"averageUtilization":80,"type":"Utilization"}},"type":"Resource"}]` | Metrics for the HPA |
| githubAdapter.hpa.minReplicas | int | `2` | Minimum number of replicas |
| githubAdapter.image.pullPolicy | string | `"IfNotPresent"` | GitHub adapter image pull policy |
| githubAdapter.image.registry | string | `"ghcr.io"` | GitHub adapter image registry |
| githubAdapter.image.repository | string | `"nissessenap/shepherd"` | GitHub adapter image repository (same binary as operator) |
| githubAdapter.image.tag | string | .Chart.AppVersion | GitHub adapter image tag (defaults to chart appVersion) |
| githubAdapter.imagePullSecrets | list | `[]` | Image pull secrets for the GitHub adapter (overrides global) |
| githubAdapter.nodeSelector | object | `{}` | Node selector for the GitHub adapter pods |
| githubAdapter.pdb.enabled | bool | `false` | Enable PodDisruptionBudget for the GitHub adapter |
| githubAdapter.pdb.maxUnavailable | string | not set | Maximum unavailable pods (mutually exclusive with minAvailable) |
| githubAdapter.pdb.minAvailable | int | `1` | Minimum available pods (mutually exclusive with maxUnavailable) |
| githubAdapter.podAnnotations | object | `{}` | Annotations for the GitHub adapter pods |
| githubAdapter.podLabels | object | `{}` | Labels for the GitHub adapter pods |
| githubAdapter.podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context for the GitHub adapter |
| githubAdapter.replicas | int | `1` | Number of GitHub adapter replicas |
| githubAdapter.resources | object | `{"limits":{"cpu":"500m","memory":"128Mi"},"requests":{"cpu":"10m","memory":"64Mi"}}` | Resource requests and limits for the GitHub adapter |
| githubAdapter.securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container security context for the GitHub adapter |
| githubAdapter.service.annotations | object | `{}` | Annotations for the GitHub adapter service |
| githubAdapter.service.port | int | `8082` | GitHub adapter webhook port |
| githubAdapter.service.type | string | `"ClusterIP"` | GitHub adapter service type |
| githubAdapter.serviceAccount.annotations | object | `{}` | Annotations to add to the GitHub adapter service account |
| githubAdapter.serviceAccount.automountServiceAccountToken | bool | `false` | Whether to auto-mount the service account token (not needed) |
| githubAdapter.serviceAccount.create | bool | `true` | Whether to create a service account for the GitHub adapter |
| githubAdapter.serviceAccount.name | string | fullname-github-adapter | The name of the GitHub adapter service account |
| githubAdapter.tolerations | list | `[]` | Tolerations for the GitHub adapter pods |
| global.additionalLabels | object | `{}` | Additional labels applied to all resources |
| global.image.registry | string | `""` | Global image registry override for all Shepherd images |
| global.imagePullSecrets | list | `[]` | Image pull secrets shared across all components |
| nameOverride | string | chart name | Overrides the chart name |
| namespaceOverride | string | .Release.Namespace | Override the release namespace |
| operator.affinity | object | `{}` | Affinity rules for the operator pods |
| operator.annotations | object | `{}` | Annotations for the operator deployment |
| operator.healthPort | int | `8082` | Health probe port |
| operator.image.pullPolicy | string | `"IfNotPresent"` | Operator image pull policy |
| operator.image.registry | string | `"ghcr.io"` | Operator image registry |
| operator.image.repository | string | `"nissessenap/shepherd"` | Operator image repository |
| operator.image.tag | string | .Chart.AppVersion | Operator image tag (defaults to chart appVersion) |
| operator.imagePullSecrets | list | `[]` | Image pull secrets for operator (overrides global) |
| operator.leaderElection | bool | `true` | Enable leader election for the operator |
| operator.metricsPort | int | `9090` | Metrics port |
| operator.nodeSelector | object | `{}` | Node selector for the operator pods |
| operator.podAnnotations | object | `{}` | Annotations for the operator pods |
| operator.podLabels | object | `{}` | Labels for the operator pods |
| operator.podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context for the operator |
| operator.rbac.create | bool | `true` | Whether to create RBAC resources for the operator |
| operator.resources | object | `{"limits":{"cpu":"500m","memory":"128Mi"},"requests":{"cpu":"10m","memory":"64Mi"}}` | Resource requests and limits for the operator |
| operator.securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container security context for the operator |
| operator.serviceAccount.annotations | object | `{}` | Annotations to add to the operator service account |
| operator.serviceAccount.create | bool | `true` | Whether to create a service account for the operator |
| operator.serviceAccount.name | string | fullname-operator | The name of the operator service account |
| operator.tolerations | list | `[]` | Tolerations for the operator pods |
| web.affinity | object | `{}` | Affinity rules for the web pods |
| web.annotations | object | `{}` | Annotations for the web deployment |
| web.hpa.enabled | bool | `false` | Enable HorizontalPodAutoscaler for the web frontend |
| web.hpa.maxReplicas | int | `5` | Maximum number of replicas |
| web.hpa.metrics | list | `[{"resource":{"name":"cpu","target":{"averageUtilization":80,"type":"Utilization"}},"type":"Resource"}]` | Metrics for the HPA |
| web.hpa.minReplicas | int | `2` | Minimum number of replicas |
| web.image.pullPolicy | string | `"IfNotPresent"` | Web image pull policy |
| web.image.registry | string | `"ghcr.io"` | Web image registry |
| web.image.repository | string | `"nissessenap/shepherd-web"` | Web image repository |
| web.image.tag | string | .Chart.AppVersion | Web image tag (defaults to chart appVersion) |
| web.imagePullSecrets | list | `[]` | Image pull secrets for the web frontend (overrides global) |
| web.nodeSelector | object | `{}` | Node selector for the web pods |
| web.pdb.enabled | bool | `false` | Enable PodDisruptionBudget for the web frontend |
| web.pdb.maxUnavailable | string | not set | Maximum unavailable pods (mutually exclusive with minAvailable) |
| web.pdb.minAvailable | int | `1` | Minimum available pods (mutually exclusive with maxUnavailable) |
| web.podAnnotations | object | `{}` | Annotations for the web pods |
| web.podLabels | object | `{}` | Labels for the web pods |
| web.podSecurityContext | object | `{"runAsNonRoot":true,"seccompProfile":{"type":"RuntimeDefault"}}` | Pod security context for the web frontend |
| web.replicas | int | `1` | Number of web frontend replicas |
| web.resources | object | `{"limits":{"cpu":"100m","memory":"64Mi"},"requests":{"cpu":"10m","memory":"32Mi"}}` | Resource requests and limits for the web frontend |
| web.securityContext | object | `{"allowPrivilegeEscalation":false,"capabilities":{"drop":["ALL"]},"readOnlyRootFilesystem":true}` | Container security context for the web frontend |
| web.service.annotations | object | `{}` | Annotations for the web service |
| web.service.port | int | `8080` | Web service port |
| web.service.type | string | `"ClusterIP"` | Web service type |
| web.serviceAccount.annotations | object | `{}` | Annotations to add to the web service account |
| web.serviceAccount.automountServiceAccountToken | bool | `false` | Whether to auto-mount the service account token (not needed for web) |
| web.serviceAccount.create | bool | `true` | Whether to create a service account for the web frontend |
| web.serviceAccount.name | string | fullname-web | The name of the web service account |
| web.tolerations | list | `[]` | Tolerations for the web pods |

