# TrueNAS CSI Driver - OpenShift Installation Guide

The TrueNAS CSI Driver runs on Red Hat OpenShift using the same Helm chart as
other Kubernetes distributions. The separate OpenShift operator is not
required. OpenShift does require SCC permissions for the privileged node
plugin, so apply the bundled SCC manifest before installing the chart.

## Prerequisites

- OpenShift 4.20 or later
- TrueNAS SCALE 25.10.0+
- A TrueNAS API key
- Network connectivity between OpenShift nodes and TrueNAS
- Helm 3+

## Install

Create the driver namespace and grant its service accounts the required SCCs:

```bash
oc new-project truenas-csi
oc apply -f deploy/openshift/scc.yaml
```

Install the Helm chart. Use the UBI image for Red Hat certification or the
standard image for ordinary OpenShift deployments.

```bash
helm upgrade --install truenas-csi oci://ghcr.io/ishioni/charts/truenas-csi \
  --namespace truenas-csi \
  --set-string config.truenasURL="wss://your-truenas.example.com/api/current" \
  --set config.truenasInsecure=true \
  --set-string config.defaultPool="tank" \
  --set-string config.nfsServer="your-truenas.example.com" \
  --set-string secret.apiKey="YOUR-TRUENAS-API-KEY" \
  --set-string image.repository="quay.io/truenas_solutions/truenas-csi"
```

For a local checkout, replace the OCI reference with
`./deploy/helm/truenas-csi`. The chart's `config`, `controller`, and `node`
values control the driver configuration and pod placement.

## Verify Installation

```bash
oc get pods -n truenas-csi
oc get csidriver csi.truenas.io
```

The controller Deployment and node DaemonSet should become ready. The node
pods must run with the SCCs from `deploy/openshift/scc.yaml`; without them,
OpenShift will reject the privileged node container or its host mounts.

## Create StorageClasses

The chart can create StorageClasses through `storageClasses` values, or they
can be applied separately:

```bash
oc apply -f examples/storageclass-nfs.yaml
oc apply -f examples/pvc-nfs.yaml
```

See the [configuration reference](configuration.md) for Helm values and
StorageClass parameters.

## Snapshots

Install the Kubernetes snapshot CRDs and controller if they are not already
present on the cluster. Then create a `VolumeSnapshotClass` with driver
`csi.truenas.io`, or configure one in the chart's `volumeSnapshotClasses`
values.

## Uninstall

Remove the Helm release and, if this SCC is no longer needed, its SCCs:

```bash
helm uninstall truenas-csi --namespace truenas-csi
oc delete -f deploy/openshift/scc.yaml --ignore-not-found
oc delete project truenas-csi
```

Deleting the Helm release does not delete existing TrueNAS datasets. Handle
those according to the StorageClass reclaim policy.

## Troubleshooting

Inspect the controller and node logs with:

```bash
oc logs -n truenas-csi deployment/truenas-csi-controller -c csi-controller
oc logs -n truenas-csi daemonset/truenas-csi-node -c csi-node
oc describe pod -n truenas-csi -l app.kubernetes.io/component=node
```

If the node pods are rejected, verify that the service accounts named in
`deploy/openshift/scc.yaml` match the names configured in the Helm release.
