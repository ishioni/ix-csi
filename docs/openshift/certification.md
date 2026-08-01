# Red Hat OpenShift Certification Guide

This document covers certifying the TrueNAS CSI driver and its UBI-based
container image for Red Hat OpenShift. The project no longer ships an
OpenShift operator or OLM bundle; deployment is through Helm.

## Supported versions

| Component     | Minimum version |
| ------------- | --------------- |
| OpenShift     | 4.20+           |
| Kubernetes    | 1.26+           |
| TrueNAS SCALE | 25.10.0+        |

## Prerequisites

- Red Hat Partner Connect account: <https://connect.redhat.com>
- OpenShift cluster for testing
- TrueNAS SCALE with a test pool
- `oc`, `helm`, and `preflight`

## Build the certified image

The UBI-based driver image is built from `Dockerfile.ubi`:

```bash
make build-ubi VERSION=1.2.0
```

Push it to the registry configured for the Red Hat certification project:

```bash
make push-ubi VERSION=1.2.0
```

The image contains the CSI driver and its storage utilities. There is no
separate operator or bundle image.

## Container certification

Create or select the TrueNAS CSI Driver container certification project in
Partner Connect, configure it for privileged host access, and run preflight:

```bash
PFLT_PYXIS_API_TOKEN="$PYXIS_API_KEY" preflight check container \
  quay.io/truenas_solutions/truenas-csi:v1.2.0 \
  --submit
```

The driver needs privileged access because its node plugin mounts filesystems,
accesses block devices, and interacts with host iSCSI/NVMe state.

## Deploy for CSI testing

Apply the SCCs and install the UBI image with Helm:

```bash
oc new-project truenas-csi
oc apply -f deploy/openshift/scc.yaml
helm upgrade --install truenas-csi oci://ghcr.io/ishioni/charts/truenas-csi \
  --namespace truenas-csi \
  --set-string image.repository=quay.io/truenas_solutions/truenas-csi \
  --set image.tag=v1.2.0 \
  --set-string config.truenasURL="wss://your-truenas.example.com/api/current" \
  --set config.truenasInsecure=true \
  --set-string config.defaultPool=tank \
  --set-string secret.apiKey="$TRUENAS_API_KEY"
```

Verify the deployment:

```bash
oc get pods -n truenas-csi
oc get csidriver csi.truenas.io
```

Install the VolumeSnapshot CRDs and snapshot controller if they are not
already present. Configure NFS and iSCSI StorageClasses and a
VolumeSnapshotClass, then run the applicable Red Hat CSI tests.

## CSI capability manifest

`deploy/openshift/csi-capabilities.yaml` documents the capabilities used for
certification testing. Update its driver version when preparing a new
certification submission.

The CSI driver name is `csi.truenas.io`. The SCC subjects must match the
service accounts created by the Helm chart:

- `truenas-csi-controller`
- `truenas-csi-node`

## Recommended test matrix

Test the following independently for NFS and iSCSI:

- dynamic provisioning and deletion
- ReadWriteOnce and ReadWriteMany where supported
- online volume expansion
- snapshots and restore from snapshots
- cloning from a PVC and from a VolumeSnapshot
- node restart and controller restart
- driver upgrade and Helm rollback

Capture the Helm values, image digest, OpenShift version, TrueNAS version,
StorageClass manifests, and test results with the certification submission.
