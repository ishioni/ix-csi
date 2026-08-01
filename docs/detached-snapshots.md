# Detached snapshots and volume clones

The driver normally uses ZFS snapshots and clones. Those objects share the
source dataset's ancestry, so TrueNAS cannot destroy the source dataset while
any dependent snapshot or clone still exists.

Detached mode uses TrueNAS replication (`zfs send`/`zfs receive`) through the
WebSocket API instead. The received dataset is independent of the source, and
the temporary source and received snapshots are removed before the CSI object
is reported ready.

## Configuration

Set `TRUENAS_DETACHED_SNAPSHOT_PARENT_DATASET` to a dataset root dedicated to
detached objects. For example:

```yaml
data:
  TRUENAS_DETACHED_SNAPSHOT_PARENT_DATASET: tank/csi-detached
```

The parent is necessary because detached snapshot IDs are represented as
datasets below it. A detached snapshot for `tank/volumes/pvc-a` named `daily`
is stored as:

```text
tank/csi-detached/tank/volumes/pvc-a/daily
```

This root must not overlap the dataset paths used for normal CSI volumes. The
driver rejects overlapping source or target paths to avoid accidentally
reintroducing a dependency or allowing a normal volume to be provisioned in
the detached namespace.

The Helm chart exposes the same setting as
`config.detachedSnapshotParentDataset`. The setting is optional until one
of the detached features is enabled.

## Per-class options

Set `detachedSnapshots: "true"` on a `VolumeSnapshotClass` to create received
datasets instead of ZFS snapshots.

Set `detachedVolumesFromSnapshots: "true"` on a `StorageClass` to create an
independent volume from either a regular or detached snapshot. Set
`detachedVolumesFromVolumes: "true"` to create an independent volume from an
existing volume. These options are independent and can be enabled separately.

Detached volume creation is implemented with the TrueNAS 25.10 WebSocket
`replication.run_onetime` job and its `core.get_jobs` status API. The driver
uses a local PUSH, transfers only the requested temporary snapshot, waits for
completion, and removes the received snapshot from the target dataset.
