# truenas-csi Helm chart

This chart packages the static `deploy/truenas-csi-driver.yaml` manifest into a configurable Helm release.

## Install

```bash
helm upgrade --install truenas-csi ./deploy/helm/truenas-csi \
  --namespace truenas-csi \
  --create-namespace \
  --set config.truenasURL=wss://truenas.example.com/api/current \
  --set config.defaultPool=SSD \
  --set secret.apiKey=REDACTED
```

## Existing secret

If you already manage credentials externally, set:

- `secret.existingSecret.name`
- `secret.existingSecret.key`

Example:

```bash
helm upgrade --install truenas-csi ./deploy/helm/truenas-csi \
  --namespace truenas-csi \
  --create-namespace \
  --set secret.existingSecret.name=truenas-api-credentials
```

The chart reads `TRUENAS_API_KEY` from the external secret using `secret.existingSecret.name` and `secret.existingSecret.key`.

## Nested dataset placement

Prefer configuring nested dataset placement in StorageClasses via `datasetPath`, for example:

```yaml
storageClasses:
  - name: truenas-iscsi
    parameters:
      protocol: iscsi
      pool: SSD
      datasetPath: talos/volumes
      fsType: ext4
```

## Non-standard kubelet roots and iSCSI paths

For Talos, K3s, or MicroK8s-style kubelet layouts, override:

- `node.kubeletRootDir`

The chart derives the plugin and registration paths from that root.

For iSCSI nodes, the chart also exposes:

- `node.iscsiDir`

`node.iscsiDir` defaults to `/etc/iscsi`, which matches the upstream manifest. On Talos, you will typically want `/var/iscsi` instead.

If the host `iscsiadm` binary is not at `/usr/sbin/iscsiadm`, set `ISCSIADM_HOST_PATH` through `node.extraEnv`. For Talos, that will typically be `/usr/local/sbin/iscsiadm`.



## Required and optional chart values

From the driver source, these `config` values are required:

- `config.truenasURL`
- `config.defaultPool`

And this secret value is required unless you use an external secret:

- `secret.apiKey`

The chart will fail to render unless either `secret.apiKey` is set or `secret.existingSecret.name` points at an existing Secret.

These `config` values are optional:

- `config.nfsServer`
- `config.iscsiPortal`
- `config.nvmeofPortal`
- `config.iscsiIQNBase`
- `config.truenasInsecure`

That means you can leave `config.nfsServer`, `config.iscsiPortal`, and `config.nvmeofPortal` empty unless you need to force those values. The driver derives NFS/iSCSI defaults from the TrueNAS URL when possible, and only requires the protocol-specific portal when you actually use that protocol.
