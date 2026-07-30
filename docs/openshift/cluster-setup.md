# OpenShift Cluster Setup (vSphere, Agent-Based Install)

This guide walks through setting up a 3-node compact OpenShift cluster on vSphere using the agent-based installer. This is the cluster configuration used for CSI certification testing.

## Prerequisites

- **openshift-install** binary matching your target OCP version (download from [console.redhat.com](https://console.redhat.com/openshift/downloads))
- **oc** CLI tool
- **Pull secret** from [console.redhat.com](https://console.redhat.com/openshift/install/pull-secret)
- **SSH key pair** (`ssh-keygen -t ed25519` if you don't have one)
- **vSphere access** with permissions to create VMs
- A network segment with static IPs available (no DHCP required)

## Network Planning

Allocate the following IPs on your vSphere network before starting:

| Purpose                  | Placeholder              | Example       |
| ------------------------ | ------------------------ | ------------- |
| API VIP (Kubernetes API) | `<API_VIP>`              | 10.220.15.200 |
| Ingress VIP (*.apps)     | `<INGRESS_VIP>`          | 10.220.15.201 |
| DNS VM                   | (static IP on the VM)    | 10.220.15.205 |
| master0                  | `<MASTER0_IP>`           | 10.220.15.210 |
| master1                  | `<MASTER1_IP>`           | 10.220.15.211 |
| master2                  | `<MASTER2_IP>`           | 10.220.15.212 |
| Gateway                  | `<GATEWAY_IP>`           | 10.220.0.1    |
| Subnet CIDR              | `<MACHINE_NETWORK_CIDR>` | 10.220.0.0/20 |
| Prefix length            | `<PREFIX_LENGTH>`        | 20            |
| DNS server               | `<DNS_SERVER_IP>`        | 10.220.15.205 |

All IPs must be on the same L2 network segment. The API and Ingress VIPs are managed by keepalived and must not be assigned to any VM.

## Step 1: Set Up DNS

OpenShift requires DNS records for `api.ocp.local`, `api-int.ocp.local`, `*.apps.ocp.local`, and each node. The simplest approach is a small VM running dnsmasq.

1. Create a minimal Ubuntu VM in vSphere (2 vCPU, 2GB RAM, 20GB disk).
2. Assign it a static IP on your network (this becomes `<DNS_SERVER_IP>`).
3. Edit `operator/ocp-install/setup-dns-vm.sh` and fill in the variables at the top:
   ```bash
   API_VIP="10.220.15.200"
   INGRESS_VIP="10.220.15.201"
   MASTER0_IP="10.220.15.210"
   MASTER1_IP="10.220.15.211"
   MASTER2_IP="10.220.15.212"
   ```
4. Copy the script to the DNS VM and run it:
   ```bash
   scp operator/ocp-install/setup-dns-vm.sh user@<DNS_VM_IP>:~
   ssh user@<DNS_VM_IP> bash setup-dns-vm.sh
   ```
5. Verify DNS resolution:
   ```bash
   dig @<DNS_VM_IP> api.ocp.local +short
   dig @<DNS_VM_IP> console-openshift-console.apps.ocp.local +short
   ```

## Step 2: Create VMs in vSphere

Create 3 virtual machines with the following specs:

| Setting         | Value                                 |
| --------------- | ------------------------------------- |
| vCPU            | 16                                    |
| RAM             | 32 GB                                 |
| Disk            | 200 GB                                |
| Firmware        | UEFI                                  |
| Network adapter | VMXNET3, connected to your port group |
| CD/DVD          | Will be connected later               |

For each VM:

1. Create the VM but do **not** power it on.
2. Record the MAC address of the network adapter (vSphere > VM > Edit Settings > Network adapter > MAC address).
3. These MAC addresses will be used as `<MASTER0_MAC>`, `<MASTER1_MAC>`, `<MASTER2_MAC>`.

## Step 3: Configure Install Manifests

The template files are in `operator/ocp-install/`. Copy them to a working directory:

```bash
mkdir ~/ocp-install
cp operator/ocp-install/install-config.yaml ~/ocp-install/
cp operator/ocp-install/agent-config.yaml ~/ocp-install/
```

### install-config.yaml

Replace the placeholders with your values:

- `<MACHINE_NETWORK_CIDR>` - your subnet CIDR (e.g. `10.220.0.0/20`)
- `<API_VIP>` - API virtual IP
- `<INGRESS_VIP>` - Ingress virtual IP
- `<MASTER0_MAC>`, `<MASTER1_MAC>`, `<MASTER2_MAC>` - VM MAC addresses
- `<PULL_SECRET>` - your pull secret JSON (single line, single-quoted)
- `<SSH_PUBLIC_KEY>` - your SSH public key

### agent-config.yaml

Replace the placeholders with your values:

- `<MASTER0_IP>`, `<MASTER1_IP>`, `<MASTER2_IP>` - static IPs for each node
- `<MASTER0_MAC>`, `<MASTER1_MAC>`, `<MASTER2_MAC>` - VM MAC addresses (must match install-config.yaml)
- `<PREFIX_LENGTH>` - subnet prefix length (e.g. `20`)
- `<DNS_SERVER_IP>` - IP of your DNS VM
- `<GATEWAY_IP>` - network gateway

## Step 4: Generate the Agent ISO

```bash
openshift-install agent create image --dir ~/ocp-install
```

This consumes the YAML files and produces `~/ocp-install/agent.x86_64.iso`.

> **Note:** `openshift-install` deletes the input YAML files after generating the ISO. Keep your original templates in `operator/ocp-install/`.

## Step 5: Boot and Install

1. Upload `agent.x86_64.iso` to a vSphere datastore.
2. For each VM, attach the ISO as a CD/DVD drive and set it as the first boot device.
3. Power on all 3 VMs.
4. Monitor the installation:
   ```bash
   openshift-install agent wait-for bootstrap-complete --dir ~/ocp-install --log-level info
   openshift-install agent wait-for install-complete --dir ~/ocp-install --log-level info
   ```

The install takes approximately 30-45 minutes. The `wait-for install-complete` command will print the kubeadmin credentials and console URL when done.

## Step 6: Verify the Cluster

```bash
export KUBECONFIG=~/ocp-install/auth/kubeconfig

# All 3 nodes should be Ready
oc get nodes

# All cluster operators should be Available
oc get co

# Check the console URL
oc whoami --show-console
```

## Troubleshooting

- **Nodes not booting**: Ensure UEFI firmware is selected (not BIOS) and the ISO is mounted as the first boot device.
- **Nodes stuck in discovery**: Verify MAC addresses in `install-config.yaml` and `agent-config.yaml` match the actual VM NICs.
- **DNS resolution failures**: Run `dig @<DNS_VM_IP> api.ocp.local` from a machine on the same network. Ensure the DNS VM's firewall allows port 53/UDP.
- **API unreachable after install**: Ensure your workstation uses the DNS VM as its resolver, or add `/etc/hosts` entries for `api.ocp.local`.
