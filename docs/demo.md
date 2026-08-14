# ix-csi - Demo Guide

This guide will help you run the ix-csi driver demo on a local Kubernetes cluster.

## Overview

The `demo-simple.sh` script provides an interactive demonstration of the ix-csi driver's capabilities:

- NFS volume provisioning
- iSCSI volume provisioning
- Volume expansion
- Volume cloning
- Volume snapshots
- Multiple volume creation
- Storage class variations

This demo runs entirely on a local Kind (Kubernetes in Docker) cluster and provisions real storage on your TrueNAS system.

## Prerequisites

Before running the demo, ensure you have the following:

### 1. Required Tools

- **Docker**: Required for Kind cluster and building container images
  - Installation: https://docs.docker.com/get-docker/
  - Verify: `docker --version`

- **Kind**: Creates a local Kubernetes cluster for testing
  - Installation: https://kind.sigs.k8s.io/docs/user/quick-start/#installation
  - Verify: `kind --version`

- **kubectl**: Kubernetes command-line tool
  - Installation: https://kubernetes.io/docs/tasks/tools/
  - Verify: `kubectl version --client`

- **Helm**: Used to install the CSI driver
  - Installation: https://helm.sh/docs/intro/install/
  - Verify: `helm version`

### 2. TrueNAS System

- **Version**: TrueNAS SCALE (tested with v25.10+)
- **Requirements**:
  - Network access from your machine to TrueNAS
  - API access enabled
  - At least one ZFS pool created (e.g., "tank")
  - API key or username/password credentials

## Configuration

### Step 1: Configure TrueNAS connection details

Set the variables consumed by the demo's Helm installation:

```bash
export TRUENAS_URL="wss://YOUR_TRUENAS_IP/api/current"
export TRUENAS_INSECURE=true
export TRUENAS_POOL=tank
export TRUENAS_NFS_SERVER="YOUR_TRUENAS_IP"
export TRUENAS_ISCSI_PORTAL="YOUR_TRUENAS_IP:3260"
export TRUENAS_ISCSI_IQN_BASE="iqn.2000-01.io.truenas"
```

TrueNAS API keys should be used over a secure WebSocket connection (`wss://`). Set `TRUENAS_INSECURE=true` for a self-signed certificate.

### Step 2: Configure API key authentication

Get your API key from TrueNAS:

- Log into TrueNAS web UI
- Click your profile icon → API Keys
- Click Add → Create new key
- Copy the generated key

```bash
export TRUENAS_API_KEY="YOUR-API-KEY"
```

## Running the Demo

Once you've configured the environment variables:

```bash
chmod +x ./demo-simple.sh
./demo-simple.sh
```

### First Run

The script will:

1. ✅ Verify prerequisites are installed
2. ✅ Confirm your TrueNAS environment variables are set
3. ✅ Create a Kind cluster with 2 worker nodes
4. ✅ Build the CSI driver Docker image
5. ✅ Load the image into the cluster
6. ✅ Install the driver with the local Helm chart
7. ✅ Set up snapshot support
8. ✅ Create StorageClasses for NFS and iSCSI
9. ✅ Launch the interactive demo menu

### Subsequent Runs

If the cluster and driver already exist, the script will:

- Skip cluster creation
- Skip driver deployment
- Go directly to the demo menu

## Demo Menu Options

The interactive menu provides these demos:

### Volume Provisioning

1. **Demo NFS volume creation** - Create a ReadWriteMany NFS volume
2. **Demo iSCSI volume creation** - Create a ReadWriteOnce iSCSI block volume
3. **Demo multiple volumes** - Create 3 volumes simultaneously
4. **Demo storage class variations** - Different compression settings

### Advanced Operations

5. **Demo volume expansion** - Expand an existing volume online
6. **Demo volume cloning** - Clone a volume (select existing or create new)
7. **Demo volume snapshots** - Create and restore from snapshots
8. **Demo clone with data verification** ⭐ - Write data, clone, verify data copied

### Inspection & Metadata

9. **Demo volume metadata inspection** - View volume attributes
10. **Demo capacity reporting** - Check volume sizes
11. **Demo topology awareness** - View node topology
12. **Demo driver capabilities** - List all CSI features

### Utilities

13. **Show current status** - View all pods, PVCs, PVs
14. **View driver logs** - Check controller and node logs
15. **Cleanup demo resources** - Delete all demo volumes

## What to Expect

### Successful Volume Creation

When you create a volume, you should see:

- ✅ PVC bound within 10-30 seconds
- ✅ New dataset appears in TrueNAS UI (Storage → Datasets)
- ✅ New share appears in TrueNAS UI (Shares → NFS or iSCSI)

### Check TrueNAS UI

After creating volumes, verify in TrueNAS:

- **Storage → Datasets** - See the new datasets (pvc-xxxxx)
- **Shares → NFS** - See NFS shares for NFS volumes
- **Shares → iSCSI** - See targets/extents for iSCSI volumes

## Advanced Configuration

### Custom IQN Prefix (Multi-Tenant)

For enterprise/multi-tenant deployments, customize IQN prefixes per StorageClass:

```yaml
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: finance-iscsi
parameters:
  protocol: "iscsi"
  iscsi.iqn-base: "iqn.2024-01.com.acme.finance"
---
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: engineering-iscsi
parameters:
  protocol: "iscsi"
  iscsi.iqn-base: "iqn.2024-01.com.acme.engineering"
```

### Self-Signed Certificates

The driver uses secure WebSocket (wss://) by default. For self-signed certificates:

```bash
export TRUENAS_URL="wss://10.0.0.136/api/current"
export TRUENAS_INSECURE=true
```

**For production with valid certificates**, set to `"false"` or remove the field:

```bash
export TRUENAS_URL="wss://truenas.example.com/api/current"
export TRUENAS_INSECURE=false
```

## Troubleshooting

### "TRUENAS_API_KEY is not set"

The demo requires TrueNAS connection details and an API key before it can install the Helm chart:

1. Set `TRUENAS_URL`, `TRUENAS_POOL`, and `TRUENAS_API_KEY`
2. Set `TRUENAS_NFS_SERVER` and `TRUENAS_ISCSI_PORTAL` if those protocols are used
3. Run the demo again

### "Failed to create driver: invalid iSCSI IQN base format"

Your `TRUENAS_ISCSI_IQN_BASE` value has an invalid format. It must:

- Start with `iqn.`
- Include date in `YYYY-MM` format
- Include reversed domain name
- Example: `iqn.2024-01.com.example`
- Example: `iqn.invalid`

### "Pool 'tank' not found"

- Verify the pool exists in TrueNAS UI (Storage → Pools)
- Update `TRUENAS_POOL` to match your actual pool name
- Pool names are case-sensitive

### "PVC stuck in Pending"

Check driver logs from the menu (Option 14) or:

```bash
kubectl logs -n ix-csi -l app.kubernetes.io/component=controller -c csi-controller
```

Common causes:

- TrueNAS not accessible from Kind cluster
- Authentication failure
- Pool doesn't exist
- Network connectivity issues

## Cleanup

### Clean Demo Resources Only

From the menu, choose **Option 15** to delete all demo PVCs while keeping the driver and cluster.

### Clean Everything

```bash
# Delete the entire Kind cluster
kind delete cluster --name ix-csi-demo
```

This removes the cluster and all associated resources. TrueNAS datasets/shares will be deleted automatically (due to `reclaimPolicy: Delete`).

## Production Deployment

For production use on a real Kubernetes cluster:

1. **Configure the Helm values** for your production settings
2. **Use a specific image tag** rather than `latest`
3. **Configure proper IQN prefix** for your organization
4. **Use TLS** (wss://) for TrueNAS connection
5. **Deploy**:
   ```bash
   helm upgrade --install ix-csi oci://ghcr.io/ishioni/charts/ix-csi \
     --namespace ix-csi \
     --create-namespace \
     --set-string config.truenasURL="wss://your-truenas.example.com/api/current" \
     --set config.truenasInsecure=false \
     --set-string config.defaultPool="tank" \
     --set-string secret.apiKey="YOUR-API-KEY"
   ```

## Getting Help

- **Report issues**: https://github.com/ishioni/ix-csi/issues
