# TrueNAS CSI Driver - OpenShift Upgrade Guide

The driver is upgraded as a Helm release. Existing volumes are not recreated
when the controller Deployment or node DaemonSet rolls out.

## Before upgrading

```bash
helm get values truenas-csi -n truenas-csi -o yaml > truenas-csi-values-backup.yaml
oc get pods -n truenas-csi
oc get pvc --all-namespaces
```

Review the release notes and ensure there are no critical volume operations in
progress.

## Upgrade

From the OCI registry:

```bash
helm upgrade truenas-csi oci://ghcr.io/ishioni/charts/truenas-csi \
  --namespace truenas-csi \
  --reuse-values \
  --version 1.2.0
```

From a checkout, replace the OCI reference with
`./deploy/helm/truenas-csi` and set `--set image.tag` to the desired driver
version. Keep the SCC manifest applied; it is independent of the Helm
release.

Monitor the rollout:

```bash
oc rollout status deployment/truenas-csi-controller -n truenas-csi
oc rollout status daemonset/truenas-csi-node -n truenas-csi
oc get pods -n truenas-csi -w
```

## Rollback

List Helm revisions and roll back to a known-good revision:

```bash
helm history truenas-csi -n truenas-csi
helm rollback truenas-csi REVISION -n truenas-csi
```

## Troubleshooting

```bash
oc logs -n truenas-csi deployment/truenas-csi-controller -c csi-controller
oc logs -n truenas-csi daemonset/truenas-csi-node -c csi-node
oc get csidriver csi.truenas.io
```

If a node pod cannot start, inspect SCC admission and verify that the service
account used by the Helm release is listed in `deploy/openshift/scc.yaml`.
