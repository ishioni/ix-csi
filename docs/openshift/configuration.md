# TrueNAS CSI Driver - OpenShift Configuration Reference

OpenShift uses the standard Helm chart configuration. There is no
OpenShift-specific custom resource or operator.

## Helm values

The most important values are:

| Value                                  | Description                                         |
| -------------------------------------- | --------------------------------------------------- |
| `config.truenasURL`                    | TrueNAS WebSocket API URL                           |
| `config.truenasInsecure`               | Skip TLS verification for self-signed certificates  |
| `config.defaultPool`                   | Default ZFS pool                                    |
| `config.nfsServer`                     | Address used for NFS mounts                         |
| `config.iscsiPortal`                   | iSCSI portal address                                |
| `config.nvmeofPortal`                  | NVMe-oF/TCP portal address                          |
| `config.iscsiIQNBase`                  | Base IQN for iSCSI targets                          |
| `config.detachedSnapshotParentDataset` | Parent dataset required for detached snapshots      |
| `secret.apiKey`                        | TrueNAS API key, unless an existing Secret is used  |
| `secret.existingSecret.name`           | Existing Secret containing `TRUENAS_API_KEY`        |
| `controller.replicas`                  | Number of controller replicas                       |
| `controller.nodeSelector`              | Controller node selector                            |
| `node.nodeSelector`                    | Node DaemonSet selector                             |
| `node.kubeletRootDir`                  | Kubelet root directory, default `/var/lib/kubelet`  |
| `node.iscsiDir`                        | iSCSI configuration directory, default `/etc/iscsi` |

Example values file:

```yaml
config:
  truenasURL: wss://truenas.example.com/api/current
  truenasInsecure: false
  defaultPool: tank
  nfsServer: 10.0.0.10
  iscsiPortal: 10.0.0.10:3260
secret:
  existingSecret:
    name: truenas-api-credentials
    key: TRUENAS_API_KEY
```

Install it with:

```bash
helm upgrade --install truenas-csi oci://ghcr.io/ishioni/charts/truenas-csi \
  --namespace truenas-csi \
  --values values.yaml
```

## SCC requirements

The node plugin needs privileged access for mount operations, block devices,
host networking, and host paths. Apply `deploy/openshift/scc.yaml` and keep
the Helm service-account names aligned with the SCC subjects.

## StorageClass parameters

StorageClass parameters are the same on OpenShift and other Kubernetes
clusters. Common parameters include:

| Parameter         | Values                           | Description                                            |
| ----------------- | -------------------------------- | ------------------------------------------------------ |
| `protocol`        | `nfs`, `iscsi`, `nvmeof`         | Storage protocol                                       |
| `pool`            | string                           | ZFS pool name                                          |
| `datasetPath`     | string                           | Dataset parent for volumes                             |
| `compression`     | `off`, `lz4`, `gzip`, `zstd`     | ZFS compression                                        |
| `sync`            | `standard`, `always`, `disabled` | ZFS sync mode                                          |
| `fsType`          | `ext4`, `xfs`                    | Filesystem for block protocols                         |
| `volblocksize`    | ZFS block size                   | iSCSI/NVMe-oF volume block size                        |
| `detachedVolumes` | `true`, `false`                  | Detach volumes created from snapshots or other volumes |

For detached snapshots, set `detachedSnapshots: "true"` on the
VolumeSnapshotClass and configure `config.detachedSnapshotParentDataset`.

## Credentials

The chart creates a Secret from `secret.apiKey` by default. To manage the
Secret separately:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: truenas-api-credentials
  namespace: truenas-csi
type: Opaque
stringData:
  TRUENAS_API_KEY: YOUR-TRUENAS-API-KEY
```

Set `secret.existingSecret.name` to `truenas-api-credentials` and leave
`secret.apiKey` empty.
