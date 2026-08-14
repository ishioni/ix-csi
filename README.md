# ix-csi

A Container Storage Interface (CSI) driver for [TrueNAS 25.10.0+](https://www.truenas.com/truenas-scale/), enabling dynamic provisioning of persistent volumes in Kubernetes using TrueNAS storage.

## Features

- **NFS volumes** - ReadWriteMany (RWX) access mode for shared storage
- **iSCSI volumes** - Block storage with ReadWriteOnce (RWO) and ReadWriteMany (RWX) access modes (RWX requires cluster filesystem like GFS2/OCFS2)
- **NVMe-oF/TCP volumes** - Block storage over NVMe over Fabrics (TCP) with optional DH-CHAP authentication
- **Dynamic provisioning** - Automatic volume creation and deletion
- **Volume expansion** - Online resize of volumes
- **Snapshots and clones** - CSI snapshot support for backup and cloning
- **CHAP authentication** - Secure iSCSI connections
- **ZFS compression** - LZ4, ZSTD, GZIP, and other algorithms
- **ZFS encryption** - Dataset-level encryption with key management
- **Automatic snapshot scheduling** - Periodic snapshots via StorageClass
- **TrueNAS Websocket API** - Uses the modern TrueNAS Websocket API

## Requirements

### TrueNAS

- TrueNAS SCALE 25.10.0+
- API access enabled
- At least one ZFS pool configured

### Kubernetes

- Kubernetes 1.26+
- For snapshots: [snapshot-controller](https://github.com/kubernetes-csi/external-snapshotter) installed

### Node Requirements

- **NFS volumes**: No additional requirements
- **iSCSI volumes**: `open-iscsi` package installed on worker nodes
- **NVMe-oF volumes**: `nvme_tcp`/`nvme_fabrics` kernel modules available on worker nodes (the node DaemonSet loads them); requires TrueNAS SCALE 25.10+ with the NVMe-oF target service enabled

## Quick Start

1. **Create an API key in TrueNAS**
   - Log into TrueNAS web UI
   - Navigate to your profile → API Keys
   - Create a new API key and copy it

2. **Configure the driver**

   ```bash
   The chart is available from the OCI registry or directly from a checkout of this repository.

   ```

3. **Deploy the driver**

   ```bash
   helm upgrade --install ix-csi oci://ghcr.io/ishioni/charts/ix-csi \
     --namespace ix-csi \
     --create-namespace \
     --set-string config.truenasURL="wss://your-truenas.example.com/api/current" \
     --set config.truenasInsecure=true \
     --set-string config.defaultPool="tank" \
     --set-string secret.apiKey="YOUR-API-KEY"
   ```

4. **Create a StorageClass and PVC**
   ```bash
   kubectl apply -f examples/storageclass-nfs.yaml
   kubectl apply -f examples/pvc-nfs.yaml
   ```

## Installation

### Prerequisites

Install the snapshot controller (required for snapshot support):

```bash
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotclasses.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshotcontents.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/client/config/crd/snapshot.storage.k8s.io_volumesnapshots.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/rbac-snapshot-controller.yaml
kubectl apply -f https://raw.githubusercontent.com/kubernetes-csi/external-snapshotter/master/deploy/kubernetes/snapshot-controller/setup-snapshot-controller.yaml
```

### Deploy the Driver

Install the Helm chart and provide your TrueNAS connection details and API key:

```bash
helm upgrade --install ix-csi oci://ghcr.io/ishioni/charts/ix-csi \
  --namespace ix-csi \
  --create-namespace \
  --set-string config.truenasURL="wss://your-truenas.example.com/api/current" \
  --set config.truenasInsecure=true \
  --set-string config.defaultPool="tank" \
  --set-string secret.apiKey="YOUR-API-KEY"
```

To install from a checkout instead, replace the OCI reference with
`./deploy/helm/ix-csi`.

### Configure StorageClasses

Create a StorageClass after installing the driver. For example:

```bash
kubectl apply -f examples/storageclass-nfs.yaml
```

### Verify Installation

```bash
# Check driver pods are running
kubectl get pods -n ix-csi

# Verify CSI driver is registered
kubectl get csidrivers
```

### Non-Standard Kubelet Paths

The Helm chart uses `/var/lib/kubelet` as the kubelet root directory by default. Some Kubernetes distributions use a different path. If your distribution uses a non-standard path, set `node.kubeletRootDir` during installation:

```bash
helm upgrade --install ix-csi oci://ghcr.io/ishioni/charts/ix-csi \
  --namespace ix-csi \
  --set node.kubeletRootDir=/var/snap/microk8s/common/var/lib/kubelet
```

| Distribution        | Kubelet Path                                |
| ------------------- | ------------------------------------------- |
| Standard Kubernetes | `/var/lib/kubelet` (default)                |
| MicroK8s            | `/var/snap/microk8s/common/var/lib/kubelet` |
| K3s                 | `/var/lib/rancher/k3s/agent/kubelet`        |

> **Important:** The `kubelet-dir` `mountPath` must match the `hostPath`. If they differ, NFS mounts will succeed inside the CSI container but will not propagate to kubelet, causing pods to see local storage instead of NFS.

#### MicroK8s Mount Propagation

MicroK8s runs inside a snap with its own mount namespace. For CSI mount propagation to work, the host root filesystem must have `shared` propagation **before** MicroK8s starts:

```bash
sudo mount --make-rshared /
microk8s start
```

To make this persistent across reboots, create a systemd unit:

```bash
sudo tee /etc/systemd/system/microk8s-mount-propagation.service <<EOF
[Unit]
Description=Ensure shared mount propagation for MicroK8s
Before=snap.microk8s.daemon-containerd.service

[Service]
Type=oneshot
ExecStart=/bin/mount --make-rshared /
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable microk8s-mount-propagation
```

## Configuration

### Driver Configuration (ConfigMap)

| Setting                         | Description                                     | Example                        |
| ------------------------------- | ----------------------------------------------- | ------------------------------ |
| `truenasURL`                    | WebSocket URL to TrueNAS API                    | `wss://10.0.0.100/api/current` |
| `truenasInsecure`               | Skip TLS verification                           | `true` (for self-signed certs) |
| `defaultPool`                   | Default ZFS pool for volumes                    | `tank`                         |
| `nfsServer`                     | NFS server address                              | `10.0.0.100`                   |
| `iscsiPortal`                   | iSCSI portal address                            | `10.0.0.100:3260`              |
| `nvmeofPortal`                  | NVMe-oF portal address (optional; auto-derived) | `10.0.0.100:4420`              |
| `iscsiIQNBase`                  | Base IQN for iSCSI targets                      | `iqn.2024-01.com.example`      |
| `detachedSnapshotParentDataset` | Dataset root for independent snapshots          | `tank/csi-detached`            |

### Prometheus Metrics

Driver-native and CSI sidecar metrics are disabled by default. Enable both
on the controller and node plugins with:

```yaml
metrics:
  enabled: true
  port: 9809
```

The chart then exposes the driver-native `/metrics` endpoint through separate
controller and headless node Services. It also enables the CSI sidecar
`--http-endpoint` metrics endpoints on the controller using ports `9801` through
`9804`.

To create Prometheus Operator scrape targets, also enable:

```yaml
metrics:
  serviceMonitor:
    enabled: true
    labels:
      release: kube-prometheus-stack
```

The driver exposes CSI RPC and TrueNAS request outcomes/latency, TrueNAS
connection and reconnect health, and provisioned capacity/volume gauges. The
main metric families are prefixed with `ix_csi_`.

The four CSI sidecars share the `csi_sidecar_operations_seconds` histogram,
with `driver_name`, `method_name`, and `grpc_status_code` labels. The
external-provisioner also exposes provision/delete counters and latency
histograms prefixed with `controller_`. The bundled Grafana dashboard includes
separate sections for driver, sidecar, and provisioner metrics.

The chart can optionally create a Prometheus Operator `ServiceMonitor`, a
Grafana sidecar-discovery ConfigMap, and a Grafana Operator `GrafanaDashboard`:

```yaml
metrics:
  serviceMonitor:
    enabled: true
  dashboards:
    enabled: true
  grafanaDashboard:
    enabled: true
```

### StorageClass Parameters

#### General Parameters

| Parameter            | Description                                                                                                                                                                                                                          | Values                                                      |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------------------------------- |
| `protocol`           | Storage protocol                                                                                                                                                                                                                     | `nfs`, `iscsi`, `nvmeof`                                    |
| `pool`               | ZFS pool (overrides default)                                                                                                                                                                                                         | pool name                                                   |
| `datasetPath`        | Parent path for volume datasets, **relative to the pool** (no pool prefix, no leading/trailing `/`, no `..`). If unset, volumes are created at the **pool root** (`pool/<pvc-name>`); e.g. `k8s/iscsi` → `pool/k8s/iscsi/<pvc-name>` | relative path                                               |
| `compression`        | ZFS compression algorithm                                                                                                                                                                                                            | `OFF`, `LZ4`, `GZIP[-1\|-9]`, `ZSTD[-1..-9]`, `ZLE`, `LZJB` |
| `sync`               | ZFS sync mode                                                                                                                                                                                                                        | `STANDARD`, `ALWAYS`, `DISABLED`                            |
| `sparse`             | Thin-provision the ZVOL (iSCSI/NVMe-oF); default `false`                                                                                                                                                                             | `true`, `false`                                             |
| `detachedVolumes`    | Create an independent volume from a snapshot or another volume using replication                                                                                                                                                     | `true`, `false`                                             |
| `datasetDescription` | Optional Helm-like Go template for the TrueNAS `org.freenas:description` user property. No property is written when this parameter is absent.                                                                                        | template string                                             |
| `zfs.<property>`     | Literal ZFS property pass-through for newly created datasets and ZVOLs; not applied to clone operations                                                                                                                              | e.g. `zfs.atime`, `zfs.recordsize`                          |

Delete-time behavior (optional): `forceDelete` (`true`/`false`) forces removal of
busy resources; `deleteExtentsWithTarget` (`true`/`false`, default `true`) removes
the iSCSI extent along with its target.

#### Dataset Metadata

`datasetDescription` is rendered only when it is explicitly present in the
StorageClass. It uses a Helm-like Go template. The template context contains:

- `.parameters`: the full CSI `CreateVolume` parameter map, including
  StorageClass parameters and provisioner-supplied values such as
  `csi.storage.k8s.io/pvc/name`, `csi.storage.k8s.io/pvc/namespace`, and
  `csi.storage.k8s.io/pv/name`.
- `.pvc.name` and `.pvc.namespace`: convenient aliases for the PVC metadata.
- `.pv.name`: a convenient alias for the PV name.

For example:

```yaml
parameters:
  datasetDescription: "{{ .pvc.namespace }}/{{ .pvc.name }}"
```

Use `index` to access parameter keys containing characters that are not valid
in Go template field syntax:

```yaml
parameters:
  datasetDescription: '{{ .parameters.foo }} / {{ index .parameters "csi.storage.k8s.io/pvc/name" }}'
```

If this value is written inside a Helm chart template, escape the inner
`{{ ... }}` delimiters so Helm emits them literally. Helm's raw-string form
can be used like this:

```yaml
datasetDescription: "{{`{{ .pvc.namespace }}/{{ .pvc.name }}`}}"
```

Literal text may be combined with template expressions. The rendered value is
written only while creating the volume dataset; idempotent retries and existing
datasets are not synchronized.

#### NFS Parameters

| Parameter          | Description                                                                                                                                                                                                        | Example                     |
| ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------- |
| `nfs.hosts`        | Allowed hosts                                                                                                                                                                                                      | `10.0.0.0/8,192.168.1.0/24` |
| `nfs.networks`     | Allowed networks                                                                                                                                                                                                   | `10.0.0.0/8`                |
| `nfs.mountOptions` | Client mount options                                                                                                                                                                                               | `hard,nfsvers=4.1`          |
| `nfs.mapAllUser`   | NFS user mapping (default: `root`)                                                                                                                                                                                 | `postgres`                  |
| `nfs.mapAllGroup`  | NFS group mapping (default: `wheel`)                                                                                                                                                                               | `postgres`                  |
| `nfs.rootSquash`   | Squash all access to the mapped user (default: `true`). Set `false` for `no_root_squash` so a pod `fsGroup` can chown the volume root — required for ownership-sensitive non-root workloads (e.g. PostgreSQL/CNPG) | `false`                     |

By default an NFS share squashes all client access to a single user (`mapall`,
`root:wheel`). Ownership-sensitive workloads that run as a non-root user (such as
PostgreSQL/CloudNativePG) need to own their data directory, which `mapall` cannot
provide. Set `nfs.rootSquash: "false"` to switch the share to `no_root_squash`:
incoming root is preserved so the kubelet (via the driver's `fsGroupPolicy: File`)
can chown the volume root to the pod's `fsGroup`, and non-root UIDs are no longer
squashed. Requires the workload to set a pod `securityContext.fsGroup`. See
`examples/storageclass-nfs-fsgroup.yaml`.

#### iSCSI Parameters

| Parameter                  | Description                                                                                  | Values                                                     |
| -------------------------- | -------------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `volblocksize`             | ZVOL block size                                                                              | `512`, `1K`, `2K`, `4K`, `8K`, `16K`, `32K`, `64K`, `128K` |
| `iscsi.blocksize`          | iSCSI logical block size                                                                     | `512`, `1024`, `2048`, `4096`                              |
| `iscsi.iqn-base`           | Override the IQN base (auto-derived from the appliance's `iscsi.global.basename` by default) | IQN string                                                 |
| `iscsi.initiators`         | Allowed initiator IQNs                                                                       | comma-separated                                            |
| `iscsi.chapUser`           | CHAP username                                                                                | string                                                     |
| `iscsi.chapSecret`         | CHAP password (12-16 chars)                                                                  | string                                                     |
| `iscsi.chapPeerUser`       | Mutual CHAP peer user                                                                        | string                                                     |
| `iscsi.chapPeerSecret`     | Mutual CHAP peer password                                                                    | string                                                     |
| `iscsi.multipathEnabled`   | Enable multipath for the session (node-side); default `false`                                | `true`, `false`                                            |
| `iscsi.persistentSessions` | Keep the iSCSI session persistent (node-side); default `false`                               | `true`, `false`                                            |

> **IPv4 only:** iSCSI portals must be IPv4. The pinned `csi-lib-iscsi` mis-parses
> IPv6 portal addresses, so iSCSI staging fails on IPv6-only clusters — use NFS
> there. The driver fails fast with a clear error if an IPv6 iSCSI portal is
> configured.

#### NVMe-oF Parameters

NVMe-oF also uses the `volblocksize` parameter above. DH-CHAP authentication is optional.

| Parameter              | Description                                | Values                                                     |
| ---------------------- | ------------------------------------------ | ---------------------------------------------------------- |
| `nvmeof.hostNQN`       | Authorized host NQN (required for DH-CHAP) | `nqn.2014-08.org.nvmexpress:uuid:...`                      |
| `nvmeof.dhchapKey`     | DH-CHAP host key                           | `DHHC-1:00:...`                                            |
| `nvmeof.dhchapCtrlKey` | Mutual DH-CHAP controller key              | `DHHC-1:00:...`                                            |
| `nvmeof.dhchapHash`    | DH-CHAP hash (default `SHA-256`)           | `SHA-256`, `SHA-384`, `SHA-512`                            |
| `nvmeof.dhchapDHGroup` | DH group                                   | `2048-BIT`, `3072-BIT`, `4096-BIT`, `6144-BIT`, `8192-BIT` |

#### Snapshot Task Parameters

| Parameter                | Description              | Values                                 |
| ------------------------ | ------------------------ | -------------------------------------- |
| `snapshot.schedule`      | Cron schedule (5 fields) | `0 0 * * *`                            |
| `snapshot.retention`     | Retention period         | `1`-`365`                              |
| `snapshot.retentionUnit` | Retention unit           | `HOUR`, `DAY`, `WEEK`, `MONTH`, `YEAR` |
| `snapshot.naming`        | Naming schema            | `auto-%Y-%m-%d_%H-%M`                  |
| `snapshot.recursive`     | Include child datasets   | `true`, `false`                        |

#### Encryption Parameters

| Parameter                | Description                | Values                       |
| ------------------------ | -------------------------- | ---------------------------- |
| `encryption`             | Enable encryption          | `true`, `false`              |
| `encryption.algorithm`   | Encryption algorithm       | `AES-256-GCM`, `AES-128-CCM` |
| `encryption.passphrase`  | Passphrase (min 8 chars)   | string                       |
| `encryption.key`         | Hex-encoded key (64 chars) | string                       |
| `encryption.generateKey` | Auto-generate key          | `true`, `false`              |

#### Credentials from a Secret

Sensitive parameters can be supplied from a Kubernetes Secret instead of inline StorageClass values, keeping them out of the StorageClass and the persisted PersistentVolume. A value from a Secret takes precedence over the matching inline parameter, so existing StorageClasses keep working unchanged.

| Purpose                                        | StorageClass reference                                      | Secret data keys                                                                           |
| ---------------------------------------------- | ----------------------------------------------------------- | ------------------------------------------------------------------------------------------ |
| ZFS encryption key/passphrase                  | `csi.storage.k8s.io/provisioner-secret-name` / `-namespace` | `encryption.key`, `encryption.passphrase`                                                  |
| iSCSI CHAP auth group (controller)             | `csi.storage.k8s.io/provisioner-secret-name` / `-namespace` | `iscsi.chapSecret`, `iscsi.chapPeerSecret`                                                 |
| iSCSI CHAP login (node)                        | `csi.storage.k8s.io/node-stage-secret-name` / `-namespace`  | `iscsi.chapUsername`, `iscsi.chapPassword`, `iscsi.chapUsernameIn`, `iscsi.chapPasswordIn` |
| NVMe-oF DH-CHAP host registration (controller) | `csi.storage.k8s.io/provisioner-secret-name` / `-namespace` | `nvmeof.dhchapKey`, `nvmeof.dhchapCtrlKey`                                                 |
| NVMe-oF DH-CHAP connection (node)              | `csi.storage.k8s.io/node-stage-secret-name` / `-namespace`  | `nvmeof.dhchapKey`, `nvmeof.dhchapCtrlKey`                                                 |

iSCSI CHAP and NVMe-oF DH-CHAP each use both a provisioner-secret (to configure TrueNAS: the CHAP auth group / the nvmet host) and a node-stage-secret (for the node's login/connection); the same Secret can serve both roles. The controller ServiceAccount is granted `get`/`list`/`watch` on `secrets` so the external-provisioner can resolve the provisioner-secret; node-stage secrets are resolved by kubelet. See `examples/storageclass-iscsi-chap-secret.yaml`, `examples/storageclass-nvmeof-dhchap-secret.yaml`, and `examples/storageclass-encrypted-secret.yaml`.

### VolumeSnapshotClass Parameters

#### Detached Snapshots

Set `detachedSnapshots: "true"` on a VolumeSnapshotClass to store snapshots as
independent received datasets. This is a VolumeSnapshotClass parameter, not a
StorageClass parameter, and requires the configured
`detachedSnapshotParentDataset` dataset root.

| Parameter           | Description                                      | Values          |
| ------------------- | ------------------------------------------------ | --------------- |
| `detachedSnapshots` | Store snapshots as independent received datasets | `true`, `false` |

## Examples

See the [`examples/`](examples/) folder for sample configurations:

- `storageclass-nfs.yaml` - Basic NFS StorageClass
- `storageclass-nfs-compressed.yaml` - NFS with ZSTD compression
- `storageclass-nfs-fsgroup.yaml` - NFS for ownership-sensitive non-root workloads (no_root_squash + pod fsGroup)
- `storageclass-iscsi.yaml` - Basic iSCSI StorageClass
- `storageclass-iscsi-chap.yaml` - iSCSI with CHAP authentication
- `storageclass-iscsi-chap-secret.yaml` - iSCSI CHAP credentials from a Secret
- `storageclass-nvmeof.yaml` - Basic NVMe-oF/TCP StorageClass
- `storageclass-nvmeof-dhchap.yaml` - NVMe-oF with DH-CHAP authentication
- `storageclass-nvmeof-dhchap-secret.yaml` - NVMe-oF DH-CHAP keys from a Secret
- `storageclass-encrypted.yaml` - Encrypted storage
- `storageclass-encrypted-secret.yaml` - Encrypted storage with the key from a Secret
- `pvc-nfs.yaml` / `pvc-iscsi.yaml` / `pvc-nvmeof.yaml` - PVC examples
- `pod-with-pvc.yaml` - Pod using a PVC
- `volumesnapshotclass.yaml` / `volumesnapshot.yaml` - Snapshot examples

## Building

### Build the binary

```bash
make build
```

### Build container images

```bash
# Build the container image
make docker-build
```

### Push to quay.io

```bash
# Login to quay.io
docker login quay.io

# Push all driver images
make push-all
```

### Run tests

```bash
make test
```

## Container Images

| Image                    | Description          |
| ------------------------ | -------------------- |
| `ghcr.io/ishioni/ix-csi` | CSI driver container |

## Running the Demo

For an interactive demonstration of all driver features using a local Kind cluster, see [docs/demo.md](docs/demo.md).

## OpenShift

The ix-csi supports Red Hat OpenShift 4.20+ and can be installed with the Helm chart. Set `openshift.enabled=true` to have the chart create the required SCCs and capabilities ConfigMap.

### Quick Start (OpenShift)

```bash
helm upgrade --install ix-csi oci://ghcr.io/ishioni/charts/ix-csi \
  --namespace ix-csi \
  --create-namespace \
  --set openshift.enabled=true \
  --set-string config.truenasURL="wss://your-truenas.example.com/api/current" \
  --set config.truenasInsecure=true \
  --set-string config.defaultPool="tank" \
  --set-string secret.apiKey="YOUR-API-KEY"
```

## Demo Scripts

Interactive demo scripts are provided to test the CSI driver:

### Standard Kubernetes (Kind)

```bash
# Set TRUENAS_URL, TRUENAS_API_KEY, and TRUENAS_POOL, then:
./demo-simple.sh
```

The OpenShift installation command is shown above; set `openshift.enabled=true` when installing the chart.

## Contributing

- Report issues: https://github.com/ishioni/ix-csi/issues
- Submit pull requests: https://github.com/ishioni/ix-csi/pulls

## License

GNU General Public License 3.0
