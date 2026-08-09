# truenas-csi

![Version: 0.0.0](https://img.shields.io/badge/Version-0.0.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.0](https://img.shields.io/badge/AppVersion-0.0.0-informational?style=flat-square)

Helm chart for deploying the TrueNAS CSI driver

**Homepage:** <https://github.com/ishioni/ix-csi>

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| truenas-csi |  |  |

## Source Code

* <https://github.com/ishioni/ix-csi>

## Requirements

Kubernetes: `>=1.26.0-0`

## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| config.defaultPool | string | `"tank"` |  |
| config.detachedSnapshotParentDataset | string | `""` |  |
| config.iscsiIQNBase | string | `"iqn.2000-01.io.truenas"` |  |
| config.iscsiPortal | string | `""` |  |
| config.nfsServer | string | `""` |  |
| config.nvmeofPortal | string | `""` |  |
| config.truenasInsecure | bool | `true` |  |
| config.truenasURL | string | `"wss://truenas.example.com/api/current"` |  |
| controller.affinity | object | `{}` |  |
| controller.extraEnv | list | `[]` |  |
| controller.extraVolumeMounts | list | `[]` |  |
| controller.extraVolumes | list | `[]` |  |
| controller.image.digest | string | `""` |  |
| controller.image.tag | string | `""` |  |
| controller.nodeSelector | object | `{}` |  |
| controller.podAnnotations | object | `{}` |  |
| controller.podLabels | object | `{}` |  |
| controller.priorityClassName | string | `""` |  |
| controller.replicas | int | `1` |  |
| controller.resources.limits.cpu | string | `"200m"` |  |
| controller.resources.limits.memory | string | `"256Mi"` |  |
| controller.resources.requests.cpu | string | `"100m"` |  |
| controller.resources.requests.memory | string | `"128Mi"` |  |
| controller.tolerations | list | `[]` |  |
| fullnameOverride | string | `""` |  |
| image.digest | string | `""` |  |
| image.pullPolicy | string | `"IfNotPresent"` |  |
| image.pullSecrets | list | `[]` |  |
| image.repository | string | `"ghcr.io/ishioni/ix-csi"` |  |
| image.tag | string | `""` |  |
| logLevel | string | `"info"` |  |
| metrics.dashboards.annotations | object | `{}` | Additional annotations for the dashboard ConfigMap. |
| metrics.dashboards.enabled | bool | `false` | Create a Grafana sidecar-discovery ConfigMap. |
| metrics.enabled | bool | `false` | Enable driver-native and CSI sidecar Prometheus endpoints and metrics Services. |
| metrics.grafanaDashboard.allowCrossNamespaceImport | bool | `true` | Allow the Grafana Operator to import the dashboard across namespaces. |
| metrics.grafanaDashboard.annotations | object | `{}` | Additional annotations for the GrafanaDashboard resource. |
| metrics.grafanaDashboard.datasources | list | `[{"datasourceName":"prometheus","inputName":"DS_PROMETHEUS"}]` | Datasource mappings used by the Grafana Operator. |
| metrics.grafanaDashboard.enabled | bool | `false` | Create a Grafana Operator GrafanaDashboard resource. |
| metrics.grafanaDashboard.folder | string | `""` | Grafana folder for the GrafanaDashboard resource. |
| metrics.grafanaDashboard.instanceSelector | object | `{"matchLabels":{"grafana.internal/instance":"grafana"}}` | Grafana Operator instance selector for the dashboard resource. |
| metrics.grafanaDashboard.labels | object | `{}` | Additional labels for the GrafanaDashboard resource. |
| metrics.grafanaDashboard.resyncPeriod | string | `"10m"` | How often the Grafana Operator re-checks the dashboard for updates. |
| metrics.port | int | `9809` | TCP port used by the driver-native Prometheus endpoint. |
| metrics.service.annotations | object | `{}` | Additional annotations for the controller and node metrics Services. |
| metrics.serviceMonitor.annotations | object | `{}` | Additional ServiceMonitor annotations. |
| metrics.serviceMonitor.enabled | bool | `false` | Create Prometheus Operator ServiceMonitor resources. |
| metrics.serviceMonitor.interval | string | `"30s"` | Prometheus scrape interval. |
| metrics.serviceMonitor.labels | object | `{}` | Additional labels for ServiceMonitor selection by Prometheus. |
| metrics.serviceMonitor.scrapeTimeout | string | `"10s"` | Prometheus scrape timeout. |
| nameOverride | string | `""` |  |
| node.affinity | object | `{}` |  |
| node.extraEnv | list | `[]` |  |
| node.extraVolumeMounts | list | `[]` |  |
| node.extraVolumes | list | `[]` |  |
| node.image.digest | string | `""` |  |
| node.image.tag | string | `""` |  |
| node.iscsiDir | string | `"/etc/iscsi"` |  |
| node.kubeletRootDir | string | `"/var/lib/kubelet"` |  |
| node.nodeSelector | object | `{}` |  |
| node.podAnnotations | object | `{}` |  |
| node.podLabels | object | `{}` |  |
| node.priorityClassName | string | `"system-node-critical"` |  |
| node.resources.limits.cpu | string | `"200m"` |  |
| node.resources.limits.memory | string | `"256Mi"` |  |
| node.resources.requests.cpu | string | `"100m"` |  |
| node.resources.requests.memory | string | `"128Mi"` |  |
| node.tolerations[0].operator | string | `"Exists"` |  |
| openshift.enabled | bool | `false` |  |
| rbac.create | bool | `true` |  |
| secret.apiKey | string | `""` |  |
| secret.existingSecret.key | string | `"TRUENAS_API_KEY"` |  |
| secret.existingSecret.name | string | `""` |  |
| secret.name | string | `"truenas-api-credentials"` |  |
| serviceAccount.controller.annotations | object | `{}` |  |
| serviceAccount.controller.create | bool | `true` |  |
| serviceAccount.controller.name | string | `""` |  |
| serviceAccount.node.annotations | object | `{}` |  |
| serviceAccount.node.create | bool | `true` |  |
| serviceAccount.node.name | string | `""` |  |
| storageClasses | list | `[]` |  |
| volumeSnapshotClasses | list | `[]` |  |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
