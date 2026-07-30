# Red Hat OpenShift Certification Guide

This document describes the process for certifying the TrueNAS CSI Driver for Red Hat OpenShift.

## Overview

Red Hat OpenShift certification involves three levels:

1. **Container Certification** - Certify individual container images
2. **Operator Certification** - Certify the operator bundle for OperatorHub
3. **CSI Certification** - Certify the CSI driver functionality (badge)

All three must be completed for full CSI certification.

## Supported Versions

| Component     | Minimum Version |
| ------------- | --------------- |
| OpenShift     | 4.20+           |
| Kubernetes    | 1.26+           |
| TrueNAS SCALE | 25.10.0+        |

## Prerequisites

- Red Hat Partner Connect account: https://connect.redhat.com
- OpenShift cluster (4.20+) for testing
- TrueNAS SCALE 25.10.0+ for integration testing
- Tools installed:
  - `preflight` - Container certification tool
  - `operator-sdk` - Operator development and testing
  - `oc` - OpenShift CLI

## Container Images

The following images must be certified:

| Image                         | Registry                  | Purpose    |
| ----------------------------- | ------------------------- | ---------- |
| `truenas-csi`                 | quay.io/truenas_solutions | CSI driver |
| `truenas-csi-operator`        | quay.io/truenas_solutions | Operator   |
| `truenas-csi-operator-bundle` | quay.io/truenas_solutions | OLM bundle |

### Building UBI-Based Images

All images must use Red Hat Universal Base Image (UBI) for certification:

```bash
# From project root
make build-all    # Build all images (driver, operator, bundle)
```

The driver uses `Dockerfile.ubi` and the operator uses `operator/Dockerfile.ubi`.

### Required Container Labels

Both driver and operator images include these required labels:

- `name` - Container name
- `vendor` - "TrueNAS"
- `version` - Semantic version
- `release` - Release number
- `summary` - Short description
- `description` - Detailed description
- `maintainer` - Contact email

### License File

Both images include `/licenses/LICENSE` (GPLv3) as required by Red Hat certification.

## Container Certification

### Step 1: Create Certification Projects

1. Log in to [Red Hat Partner Connect](https://connect.redhat.com)
2. Navigate to **Product Certification** > **Manage Products**
3. Create a new certification project for each container:
   - TrueNAS CSI Driver
   - TrueNAS CSI Operator

### Step 2: Configure Driver as Privileged

The CSI driver requires root privileges for mount operations:

1. Go to the driver certification project **Settings** tab
2. Under **Host level access**, select **Privileged**
3. Save changes

This exempts the container from the `RunAsNonRoot` check.

### Step 3: Run Preflight Checks and Submit

Run preflight with `--submit` to check and submit results in one step.
The Pyxis API token is available from Red Hat Partner Connect under **Account Settings**.
Set it as the `PYXIS_API_KEY` environment variable.

```bash
# Submit driver image (project: 6984e4411c8ca46590f85669)
PFLT_PYXIS_API_TOKEN="$PYXIS_API_KEY" preflight check container \
  quay.io/truenas_solutions/truenas-csi:v1.0.0 \
  --submit \
  --certification-component-id=6984e4411c8ca46590f85669

# Submit operator image (project: 6984f4f22806a3280f06bc67)
PFLT_PYXIS_API_TOKEN="$PYXIS_API_KEY" preflight check container \
  quay.io/truenas_solutions/truenas-csi-operator:v1.0.0 \
  --submit \
  --certification-component-id=6984f4f22806a3280f06bc67
```

Both images should report `Preflight result: PASSED`.

**Note:** When run with `--submit` and the correct project ID, the driver image passes all
checks (including `RunAsNonRoot`) because the project is configured as Privileged in Partner Connect.
Without `--submit`, `RunAsNonRoot` will show as FAILED — this is expected and can be ignored for local testing.

## Operator Certification

### Step 1: Validate Bundle

Run the OLM scorecard tests:

```bash
cd operator
operator-sdk scorecard bundle/
```

All 5 tests must pass:

- `olm-bundle-validation`
- `olm-crds-have-validation`
- `olm-crds-have-resources`
- `olm-spec-descriptors`
- `olm-status-descriptors`

### Step 2: Run Tests

```bash
# Unit tests
make test

# E2E tests (creates Kind cluster)
make test-e2e

# Integration tests (requires CRC/OpenShift + TrueNAS)
make test-integration TRUENAS_IP=<ip> TRUENAS_API_KEY=<key>
```

### Step 3: Create Operator Project

1. In Partner Connect, create an **Operator** certification project
2. Link the certified container images
3. Upload the bundle or provide the bundle image reference

### Step 4: Submit for Review

Push the bundle image and submit:

```bash
make bundle-push
```

## CSI Certification (Badge)

CSI certification is a functional badge (cert ID on Red Hat Connect) that validates storage
capabilities by running the OpenShift E2E CSI test suite against the driver.

### Prerequisites

Before CSI certification:

1. Container images must be certified and published (or at least submitted)
2. Operator must be certified and published (or at least submitted)
3. An OpenShift cluster with the driver deployed and working (CRC is sufficient)
4. TrueNAS SCALE with a pool that has enough free space (~100 GiB recommended)
5. StorageClasses and a VolumeSnapshotClass configured for each protocol

### Test Environment Setup

Ensure the following are deployed on the OpenShift cluster:

```bash
# Verify driver pods are running
oc get pods -n truenas-csi

# Verify StorageClasses exist
oc get sc truenas-nfs truenas-iscsi

# Verify VolumeSnapshotClass exists
oc get volumesnapshotclass truenas-snapclass

# VolumeSnapshot CRDs and snapshot controller must be installed
# (CRC does not include these by default)
oc get crd volumesnapshots.snapshot.storage.k8s.io
```

### Test Manifests

Tests are run separately for each protocol (NFS and iSCSI). Each run uses a manifest
file that declares the driver's capabilities for that protocol.

**NFS manifest** (`manifest-nfs.yaml`):

```yaml
ShortName: truenas-nfs
StorageClass:
  FromExistingClassName: truenas-nfs
SnapshotClass:
  FromExistingClassName: truenas-snapclass
DriverInfo:
  Name: csi.truenas.io
  Capabilities:
    persistence: true
    exec: true
    multipods: true
    RWX: true
    block: false
    fsGroup: true
    snapshotDataSource: true
    pvcDataSource: true
    controllerExpansion: true
    nodeExpansion: false
    volumeLimits: false
    topology: true
```

**iSCSI manifest** (`manifest-iscsi.yaml`):

```yaml
ShortName: truenas-iscsi
StorageClass:
  FromExistingClassName: truenas-iscsi
SnapshotClass:
  FromExistingClassName: truenas-snapclass
DriverInfo:
  Name: csi.truenas.io
  Capabilities:
    persistence: true
    exec: true
    multipods: true
    RWX: false
    block: true
    fsGroup: true
    snapshotDataSource: true
    pvcDataSource: true
    controllerExpansion: true
    nodeExpansion: true
    volumeLimits: false
    singleNodeVolume: true
    topology: true
```

Key differences between protocols:

- NFS: `RWX: true`, `block: false`, `nodeExpansion: false` (server-side only)
- iSCSI: `RWX: false`, `block: true`, `nodeExpansion: true`, `singleNodeVolume: true`

### Running CSI Certification Tests

Tests are run using the containerized `ose-tests` image from Red Hat's registry.
You must run the tests **separately for each protocol** since they use different manifests.

#### Step 1: Prepare the kubeconfig

```bash
# Copy kubeconfig to a working directory
cp ~/.kube/config /path/to/workdir/kubeconfig.yaml
```

#### Step 2: Run tests for each protocol

Use `--network=host` so the test container can reach the cluster API.
Use `--max-parallel-tests=1` for serialized execution (prevents overloading the cluster).

```bash
# NFS tests
docker run --rm --network=host \
  -v /path/to/workdir/kubeconfig.yaml:/kubeconfig.yaml:z \
  -v /path/to/workdir/manifest-nfs.yaml:/manifest.yaml:z \
  -v /path/to/workdir/results-nfs:/results:z \
  registry.redhat.io/openshift4/ose-tests-rhel9:v4.20 \
  openshift-tests run openshift/csi \
    --provider '{"type":"skeleton"}' \
    --max-parallel-tests=1 \
    --timeout 20m \
    --junit-dir /results \
    -o /results/results.txt \
    --file /manifest.yaml

# iSCSI tests
docker run --rm --network=host \
  -v /path/to/workdir/kubeconfig.yaml:/kubeconfig.yaml:z \
  -v /path/to/workdir/manifest-iscsi.yaml:/manifest.yaml:z \
  -v /path/to/workdir/results-iscsi:/results:z \
  registry.redhat.io/openshift4/ose-tests-rhel9:v4.20 \
  openshift-tests run openshift/csi \
    --provider '{"type":"skeleton"}' \
    --max-parallel-tests=1 \
    --timeout 20m \
    --junit-dir /results \
    -o /results/results.txt \
    --file /manifest.yaml
```

**Notes:**

- The `ose-tests` image requires a Red Hat registry pull secret (`docker login registry.redhat.io`)
- Serialized execution (`--max-parallel-tests=1`) takes longer (~45m NFS, ~1h iSCSI) but produces clean results
- The `--timeout 20m` flag gives extra per-test headroom for slower environments
- Result files will be owned by root; run `sudo chown -R $(id -u):$(id -g) results-*/` after

#### Step 3: Verify results

Check the JUnit XML for failures:

```bash
# Should show failures="0" (ignore the MonitorTest failure — it's a platform-level check, not CSI)
grep 'testsuite.*failures' results-nfs/junit_e2e__*.xml
grep 'testsuite.*failures' results-iscsi/junit_e2e__*.xml
```

Expected results:

- **NFS**: ~55 pass, 0 fail, ~227 skip
- **iSCSI**: ~74 pass, 0 fail, ~206 skip

The `e2e-monitor-tests__*.xml` file contains platform monitor failures (API server disruption, etc.)
which are CRC infrastructure issues, not CSI driver failures. These do not affect certification.

### Submitting CSI Test Results

CSI functional test results are submitted through the Red Hat Connect web portal.

#### Step 1: Package the results

```bash
cd operator/certification-results

tar czf truenas-csi-functional-cert-results.tar.gz \
  manifest-iscsi.yaml \
  manifest-nfs.yaml \
  iscsi/junit_e2e__*.xml \
  nfs/junit_e2e__*.xml
```

Include only the `junit_e2e__*.xml` files (the actual CSI test results) and the manifests.
The `e2e-monitor-tests__*.xml` and `results.txt` files are not required for submission.

#### Step 2: Upload to Red Hat Connect

1. Log in to [Red Hat Partner Connect](https://connect.redhat.com)
2. Navigate to your CSI functional certification project
3. On the **Summary** tab, go to the **Files** section
4. Click **Upload** and attach the tarball
5. Add a description, e.g.: `TrueNAS CSI v1.0.0 - OpenShift 4.20 e2e results: iSCSI (74 pass/0 fail) and NFS (55 pass/0 fail) JUnit XMLs + test manifests`
6. Optionally add comments in the **Discussions** section explaining the test environment

#### Step 3: Wait for review

A Red Hat certification engineer will manually review:

- The JUnit XMLs to verify 0 test failures
- The test manifests to confirm declared capabilities match the results
- That the tests were run on a supported OpenShift version

Once approved, the CSI functional certification moves to "completed/published" status.
This is a prerequisite for the operator bundle to pass the Hydra `query-publishing-checklist`.

## Certification Maintenance

### Version Updates

When releasing a new version:

1. Update `VERSION` in `Makefile` and `operator/Makefile`
2. Update `version` labels in `Dockerfile.ubi` and `operator/Dockerfile.ubi`
3. Build, push, and tag all images:
   ```bash
   make release VERSION=x.y.z
   ```
4. Regenerate the OLM bundle (resolves image digests automatically):
   ```bash
   cd operator && make bundle USE_IMAGE_DIGESTS=true
   ```
5. Build and push the bundle image:
   ```bash
   make bundle-build bundle-push
   ```
6. Submit preflight results for driver and operator images (see Container Certification above)
7. Re-run CSI e2e tests and submit results (see CSI Certification above)

### OpenShift Version Support

- Certifications are specific to OpenShift minor versions
- Recertify for each new OpenShift minor release
- Certifications valid for 12 months or until OpenShift version EOL

### Annual Recertification

Red Hat requires annual recertification:

1. Re-run all certification tests
2. Update documentation for any changes
3. Submit recertification through Partner Connect

## Troubleshooting

### Preflight Failures

| Check                     | Common Fix                                             |
| ------------------------- | ------------------------------------------------------ |
| `RunAsNonRoot`            | Set "Privileged" in project settings (CSI driver only) |
| `HasLicense`              | Ensure `/licenses/LICENSE` exists in image             |
| `HasRequiredLabel`        | Add missing labels to Dockerfile                       |
| `BasedOnUbi`              | Use UBI base image                                     |
| `HasNoProhibitedPackages` | Remove RHEL kernel packages                            |

### Scorecard Failures

| Test                      | Common Fix                             |
| ------------------------- | -------------------------------------- |
| `olm-spec-descriptors`    | Add x-descriptors to CRD spec fields   |
| `olm-status-descriptors`  | Add x-descriptors to CRD status fields |
| `olm-crds-have-resources` | Add resources list to CSV              |

### CSI Test Failures

1. Check driver logs: `oc logs -n truenas-csi deploy/truenas-csi-controller`
2. Check node logs: `oc logs -n truenas-csi ds/truenas-csi-node`
3. Verify TrueNAS connectivity
4. Check StorageClass and VolumeSnapshotClass configuration

## References

- [Red Hat Partner Connect](https://connect.redhat.com)
- [Red Hat Software Certification Guide](https://docs.redhat.com/en/documentation/red_hat_software_certification/2025/html/red_hat_software_certification_workflow_guide/)
- [OpenShift CSI Certification Policy](https://docs.redhat.com/en/documentation/red_hat_software_certification/2025/html-single/red_hat_openshift_software_certification_policy_guide/)
- [Preflight Documentation](https://github.com/redhat-openshift-ecosystem/openshift-preflight)
- [Operator SDK Scorecard](https://sdk.operatorframework.io/docs/testing-operators/scorecard/)
- [CSI Specification](https://github.com/container-storage-interface/spec)

## Quick Reference

### Red Hat Connect Project IDs

| Image                                  | Project ID                 |
| -------------------------------------- | -------------------------- |
| Driver (`truenas-csi`)                 | `6984e4411c8ca46590f85669` |
| Operator (`truenas-csi-operator`)      | `6984f4f22806a3280f06bc67` |
| Bundle (`truenas-csi-operator-bundle`) | `6984fe14a9bd925b3f2f2502` |

### Build and Push All Images

```bash
# Build and push everything including 'latest' tag
make release VERSION=1.0.0

# Regenerate bundle with digests, then build and push
cd operator && make bundle USE_IMAGE_DIGESTS=true
cd .. && make bundle-build bundle-push
```

### Submit Container Preflight Results

```bash
# Requires PYXIS_API_KEY environment variable
PFLT_PYXIS_API_TOKEN="$PYXIS_API_KEY" preflight check container \
  quay.io/truenas_solutions/truenas-csi:v1.0.0 \
  --submit --certification-component-id=6984e4411c8ca46590f85669

PFLT_PYXIS_API_TOKEN="$PYXIS_API_KEY" preflight check container \
  quay.io/truenas_solutions/truenas-csi-operator:v1.0.0 \
  --submit --certification-component-id=6984f4f22806a3280f06bc67
```

### Certification Checklist

- [ ] Container images built with UBI base (`make build-ubi`, `make operator-build`)
- [ ] Required labels added to all images (name, vendor, version, release, summary, description, maintainer)
- [ ] License file at `/licenses/LICENSE` in both images
- [ ] Driver set as "Privileged" in Partner Connect settings
- [ ] Preflight passes and submitted for driver and operator images
- [ ] OLM bundle generated with image digests (`make bundle USE_IMAGE_DIGESTS=true`)
- [ ] Scorecard passes (`operator-sdk scorecard bundle/`)
- [ ] CSI E2E tests pass for NFS (0 failures)
- [ ] CSI E2E tests pass for iSCSI (0 failures)
- [ ] Test results packaged and uploaded to CSI functional certification on Partner Connect
- [ ] Operator bundle PR submitted to `redhat-openshift-ecosystem/certified-operators`
