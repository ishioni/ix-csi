package driver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/go-logr/logr"
	iscsilib "github.com/kubernetes-csi/csi-lib-iscsi/iscsi"

	"k8s.io/mount-utils"
)

// StorageClass parameter keys for iSCSI configuration
const (
	paramCHAPUsername       = "iscsi.chapUsername"
	paramCHAPPassword       = "iscsi.chapPassword"
	paramCHAPUsernameIn     = "iscsi.chapUsernameIn"
	paramCHAPPasswordIn     = "iscsi.chapPasswordIn"
	paramMultipathEnabled   = "iscsi.multipathEnabled"
	paramPersistentSessions = "iscsi.persistentSessions"

	// iSCSI connection settings
	iscsiRetryCount    = 10 // number of login attempts
	iscsiCheckInterval = 1  // seconds between retries

	// iscsiadmExitNoObjsFound is iscsiadm's ISCSI_ERR_NO_OBJS_FOUND: the record or
	// session asked about does not exist.
	iscsiadmExitNoObjsFound = 21

	// Filesystem types
	fsTypeXFS = "xfs"

	// Mount options
	mountOptionNouuid = "nouuid"
	mountOptionBind   = "bind"
)

// ISCSIHandler implements the ProtocolHandler interface for iSCSI volumes
type ISCSIHandler struct {
	mounter *mount.SafeFormatAndMount
	resizer *mount.ResizeFs
	log     logr.Logger

	// Tests can replace device rescans without accessing host sysfs.
	// A nil override uses rescanExpandDevice.
	expandDevice func(devicePath string, blockDevices []string) error
}

// ISCSIConfig holds iSCSI-specific configuration parsed from volume/publish contexts
type ISCSIConfig struct {
	TargetPortal       string
	TargetIQN          string
	LUN                int32
	CHAPUsername       string
	CHAPPassword       string
	CHAPUsernameIn     string
	CHAPPasswordIn     string
	MultipathEnabled   bool
	PersistentSessions bool
}

// NewISCSIHandler creates a new iSCSI protocol handler.
func NewISCSIHandler(mounter *mount.SafeFormatAndMount, log logr.Logger) (*ISCSIHandler, error) {
	return &ISCSIHandler{
		mounter: mounter,
		resizer: mount.NewResizeFs(mounter.Exec),
		log:     log,
	}, nil
}

// Protocol returns the protocol name
func (h *ISCSIHandler) Protocol() string {
	return ProtocolISCSI
}

// parseISCSIConfig extracts iSCSI configuration from publish and volume contexts.
// CHAP credentials are sourced from the CSI node-stage secret when present,
// falling back to the volume context (StorageClass parameters) for backward
// compatibility. Using a secret keeps the credentials out of the persisted
// PersistentVolume volume context.
func parseISCSIConfig(publishContext, volumeContext, secrets map[string]string) (*ISCSIConfig, error) {
	config := &ISCSIConfig{
		TargetPortal: publishContext[PublishContextTargetPortal],
		TargetIQN:    publishContext[PublishContextTargetIQN],
	}

	// Parse LUN
	if lunStr := publishContext[PublishContextLUN]; lunStr != "" {
		lun, err := strconv.ParseInt(lunStr, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("invalid LUN value: %v", err)
		}
		config.LUN = int32(lun)
	}

	// CHAP credentials from the node-stage secret or, as a fallback, the volume
	// context (StorageClass parameters).
	config.CHAPUsername = secretOrParam(secrets, volumeContext, paramCHAPUsername)
	config.CHAPPassword = secretOrParam(secrets, volumeContext, paramCHAPPassword)
	config.CHAPUsernameIn = secretOrParam(secrets, volumeContext, paramCHAPUsernameIn)
	config.CHAPPasswordIn = secretOrParam(secrets, volumeContext, paramCHAPPasswordIn)

	// Multipath and persistent sessions
	if val := volumeContext[paramMultipathEnabled]; strings.EqualFold(val, "true") {
		config.MultipathEnabled = true
	}
	if val := volumeContext[paramPersistentSessions]; strings.EqualFold(val, "true") {
		config.PersistentSessions = true
	}

	return config, nil
}

// buildConnector creates a csi-lib-iscsi Connector from our config
func (h *ISCSIHandler) buildConnector(volumeID string, config *ISCSIConfig) *iscsilib.Connector {
	connector := &iscsilib.Connector{
		VolumeName:    volumeID,
		TargetIqn:     config.TargetIQN,
		TargetPortals: []string{config.TargetPortal},
		Lun:           config.LUN,
		RetryCount:    iscsiRetryCount,
		CheckInterval: iscsiCheckInterval,
		DoDiscovery:   true,
	}

	// Configure CHAP authentication.
	//
	// The controller already resolves the exact target IQN and portal, so
	// sendtargets discovery is unnecessary — and TrueNAS permits only one
	// CHAP_MUTUAL discovery entry system-wide, so discovery auth cannot be scaled
	// per volume. For CHAP volumes we therefore skip discovery (DoDiscovery=false)
	// and log in directly to the known target, enforcing CHAP only at the session
	// scope (node.session.auth). csi-lib-iscsi applies session CHAP via
	// CreateDBEntry, which it calls only when DoCHAPDiscovery is set, and only
	// writes credentials when Secrets.SecretsType == "chap"; Connector.AuthType is
	// not consulted. No DiscoverySecrets are set since we do not discover.
	// (Non-CHAP volumes keep the default discovery path.)
	if config.CHAPUsername != "" && config.CHAPPassword != "" {
		connector.DoDiscovery = false
		connector.DoCHAPDiscovery = true
		connector.AuthType = iscsiAuthTypeCHAP
		connector.SessionSecrets = iscsilib.Secrets{
			SecretsType: iscsiAuthTypeCHAP,
			UserName:    config.CHAPUsername,
			Password:    config.CHAPPassword,
			UserNameIn:  config.CHAPUsernameIn,
			PasswordIn:  config.CHAPPasswordIn,
		}
	}

	return connector
}

// ensureIPv4Portal rejects IPv6 iSCSI portals with an actionable error. The pinned
// csi-lib-iscsi mis-parses IPv6 portal addresses (it splits the portal on ":"), so
// the device by-path it waits for never matches the real udev symlink and staging
// fails after many retries with an opaque "failed to find device path". Fail fast
// with a clear message instead. IPv6-only clusters should use NFS.
func ensureIPv4Portal(portal string) error {
	host := portal
	if h, _, err := net.SplitHostPort(portal); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		return fmt.Errorf("iSCSI portal %q is IPv6, which is not supported (csi-lib-iscsi mis-parses IPv6 portals) — use an IPv4 portal, or NFS on IPv6-only clusters", portal)
	}
	return nil
}

func withCommandOutput(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
	}
	return err
}

// Stage implements iSCSI volume staging (login and device setup)
func (h *ISCSIHandler) Stage(ctx context.Context, req *StageRequest) (*StageResult, error) {
	h.log.V(LogLevelDebug).Info("iSCSI Stage", "volumeId", req.VolumeID, "stagingPath", req.StagingPath, "isBlock", req.IsBlockVolume)

	// Parse iSCSI configuration
	config, err := parseISCSIConfig(req.PublishContext, req.VolumeContext, req.Secrets)
	if err != nil {
		return nil, fmt.Errorf("failed to parse iSCSI config: %w (check publish context from controller)", err)
	}

	if config.TargetPortal == "" || config.TargetIQN == "" {
		return nil, fmt.Errorf("iSCSI target portal and IQN are required (check StorageClass parameters and controller publish context)")
	}

	if err := ensureIPv4Portal(config.TargetPortal); err != nil {
		return nil, err
	}

	// Build connector for csi-lib-iscsi
	connector := h.buildConnector(req.VolumeID, config)

	// Connect to iSCSI target
	h.log.V(LogLevelDebug).Info("Connecting to iSCSI target", "portal", config.TargetPortal, "iqn", config.TargetIQN, "lun", config.LUN)
	devicePath, err := iscsilib.Connect(*connector)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to iSCSI target %s at %s: %w", config.TargetIQN, config.TargetPortal, withCommandOutput(err))
	}

	h.log.V(LogLevelDebug).Info("iSCSI connected", "device", devicePath)

	// Raw block volumes get a durable staging bind mount. Publish binds from this
	// anchor rather than caching a kernel device name that may be reused later.
	if req.IsBlockVolume {
		if err := stageBlockDevice(h.mounter, devicePath, req.StagingPath); err != nil {
			return nil, err
		}
		h.log.V(LogLevelDebug).Info("iSCSI block volume staged", "volumeId", req.VolumeID, "device", devicePath)
		return &StageResult{DevicePath: devicePath}, nil
	}

	// Get filesystem type for mount volumes
	fsType := req.FSType
	if fsType == "" {
		fsType = DefaultFSType
	}

	// Create staging directory
	if err := os.MkdirAll(req.StagingPath, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}

	// For XFS, add nouuid mount option to allow mounting cloned volumes
	// that share the same UUID as the source volume on the same node.
	mountFlags := req.MountFlags
	if fsType == fsTypeXFS && !slices.Contains(mountFlags, mountOptionNouuid) {
		mountFlags = append(mountFlags, mountOptionNouuid)
	}

	// Format and mount device using SafeFormatAndMount
	h.log.V(LogLevelDebug).Info("FormatAndMount device", "device", devicePath, "stagingPath", req.StagingPath, "fsType", fsType)
	if err := h.mounter.FormatAndMount(devicePath, req.StagingPath, fsType, mountFlags); err != nil {
		return nil, fmt.Errorf("failed to format and mount device: %w", err)
	}

	// Resize filesystem if the block device is larger (e.g., snapshot restored
	// to a larger PVC). FormatAndMount skips formatting when a filesystem
	// already exists, so the filesystem may be smaller than the ZVOL.
	if needsResize, err := h.resizer.NeedResize(devicePath, req.StagingPath); err == nil && needsResize {
		h.log.Info("Filesystem smaller than device, resizing", "volumeId", req.VolumeID, "device", devicePath)
		if _, err := h.resizer.Resize(devicePath, req.StagingPath); err != nil {
			return nil, fmt.Errorf("failed to resize filesystem after mount: %w", err)
		}
	}

	h.log.V(LogLevelDebug).Info("iSCSI volume staged", "volumeId", req.VolumeID, "stagingPath", req.StagingPath)
	return &StageResult{DevicePath: devicePath}, nil
}

// Unstage disconnects the live iSCSI sessions discovered by NodeUnstageVolume.
// The staging mount is removed centrally before this runs so a failed logout can be
// retried by finding the deterministic target IQN in the live session table.
func (h *ISCSIHandler) Unstage(ctx context.Context, req *UnstageRequest) error {
	h.log.V(LogLevelDebug).Info("iSCSI Unstage", "volumeId", req.VolumeID)
	if req.ISCSI == nil || req.ISCSI.TargetIQN == "" {
		return fmt.Errorf("missing live iSCSI connection identity for volume %s", req.VolumeID)
	}
	if err := logoutISCSITarget(req.ISCSI.TargetIQN, req.ISCSI.Portals); err != nil {
		return fmt.Errorf("failed to log out of iSCSI target %s: %w", req.ISCSI.TargetIQN, err)
	}
	connected, err := iscsiTargetConnected(req.ISCSI.TargetIQN)
	if err != nil {
		return fmt.Errorf("failed to verify iSCSI logout for %s: %w", req.ISCSI.TargetIQN, err)
	}
	if connected {
		return fmt.Errorf("iSCSI target %s remains connected after logout", req.ISCSI.TargetIQN)
	}
	h.log.V(LogLevelDebug).Info("Disconnected from iSCSI target", "targetIqn", req.ISCSI.TargetIQN)
	return nil
}

// logoutISCSITarget logs out of every portal of a target and drops its node
// database entry. csi-lib-iscsi's Disconnect helper does the same work but discards
// every error and gives the caller no way to tell a completed logout from a failed
// one, so its finer-grained calls are used directly here. Overridable in tests.
var (
	iscsiLogout        = iscsilib.Logout
	iscsiDeleteDBEntry = iscsilib.DeleteDBEntry
	logoutISCSITarget  = logoutISCSITargetImpl
)

func logoutISCSITargetImpl(targetIQN string, portals []string) error {
	for _, portal := range portals {
		// Keep the complete portal. iscsiadm node records include the port, and
		// dropping a non-default port can log out the wrong record or no record.
		if err := iscsiLogout(targetIQN, portal); err != nil && !isNoISCSIObjectsFound(err) {
			return fmt.Errorf("logout from portal %s failed: %w", portal, err)
		}
	}

	if err := iscsiDeleteDBEntry(targetIQN); err != nil && !isNoISCSIObjectsFound(err) {
		return fmt.Errorf("removing the node database entry failed: %w", err)
	}
	return nil
}

// isNoISCSIObjectsFound reports whether an iscsiadm call failed only because what it
// was asked about is not present, which makes logout idempotent.
func isNoISCSIObjectsFound(err error) bool {
	var exitErr *exec.ExitError
	return errors.As(err, &exitErr) && exitErr.ExitCode() == iscsiadmExitNoObjsFound
}

// Publish implements iSCSI volume publishing (bind mount from staging)
func (h *ISCSIHandler) Publish(ctx context.Context, req *PublishRequest) error {
	h.log.V(LogLevelDebug).Info("iSCSI Publish", "volumeId", req.VolumeID, "stagingPath", req.StagingPath, "targetPath", req.TargetPath, "isBlock", req.IsBlockVolume)

	// Handle block volume publishing
	if req.IsBlockVolume {
		return h.publishBlockVolume(ctx, req)
	}

	if req.StagingPath == "" {
		return fmt.Errorf("staging path is required for iSCSI mount volumes")
	}

	// Verify staging path is mounted
	notMounted, err := h.mounter.IsLikelyNotMountPoint(req.StagingPath)
	if err != nil || notMounted {
		return fmt.Errorf("volume not staged at %s", req.StagingPath)
	}

	// Create target directory
	if err := os.MkdirAll(req.TargetPath, 0o750); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	// Bind mount from staging to target
	mountOptions := []string{mountOptionBind}
	if req.ReadOnly {
		mountOptions = append(mountOptions, mountOptionReadOnly)
	}

	if err := h.mounter.Mount(req.StagingPath, req.TargetPath, "", mountOptions); err != nil {
		return fmt.Errorf("failed to bind mount: %w", err)
	}

	h.log.V(LogLevelDebug).Info("iSCSI volume published", "volumeId", req.VolumeID, "targetPath", req.TargetPath)
	return nil
}

// publishBlockVolume bind-mounts the staged raw-block anchor to the workload.
func (h *ISCSIHandler) publishBlockVolume(ctx context.Context, req *PublishRequest) error {
	if err := publishBlockDevice(h.mounter, req.StagingPath, req.TargetPath, req.ReadOnly); err != nil {
		return err
	}
	h.log.V(LogLevelDebug).Info("iSCSI block volume published", "volumeId", req.VolumeID, "stagingPath", req.StagingPath, "targetPath", req.TargetPath)
	return nil
}

// Unpublish implements iSCSI volume unpublishing
func (h *ISCSIHandler) Unpublish(ctx context.Context, req *UnpublishRequest) error {
	h.log.V(LogLevelDebug).Info("iSCSI Unpublish", "volumeId", req.VolumeID, "targetPath", req.TargetPath)

	notMounted, err := h.mounter.IsLikelyNotMountPoint(req.TargetPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to check mount point: %w", err)
	}

	if notMounted {
		return nil
	}

	if err := h.mounter.Unmount(req.TargetPath); err != nil {
		return fmt.Errorf("failed to unmount: %w", err)
	}

	os.Remove(req.TargetPath)

	h.log.V(LogLevelDebug).Info("iSCSI volume unpublished", "volumeId", req.VolumeID, "targetPath", req.TargetPath)
	return nil
}

// rescanExpandDevice refreshes every live SCSI path and the multipath map, if any.
func (h *ISCSIHandler) rescanExpandDevice(devicePath string, blockDevices []string) error {
	h.rescanSCSIHosts()

	var errs []error
	for _, blockDevice := range blockDevices {
		rescanPath := filepath.Join(sysClassBlockDir, filepath.Base(blockDevice), "device", "rescan")
		if err := os.WriteFile(rescanPath, []byte("1\n"), 0o200); err != nil {
			errs = append(errs, fmt.Errorf("failed to rescan %s: %w", blockDevice, err))
		}
	}
	if strings.HasPrefix(filepath.Base(devicePath), "dm-") {
		device := &iscsilib.Device{Name: filepath.Base(devicePath)}
		if err := iscsilib.ResizeMultipathDevice(device); err != nil {
			errs = append(errs, fmt.Errorf("failed to resize multipath device %s: %w", devicePath, err))
		}
	}
	return errors.Join(errs...)
}

// Expand implements iSCSI volume expansion.
func (h *ISCSIHandler) Expand(ctx context.Context, req *ExpandRequest) (*ExpandResult, error) {
	h.log.V(LogLevelDebug).Info("iSCSI Expand", "volumeId", req.VolumeID, "volumePath", req.VolumePath)

	if req.DevicePath == "" || len(req.BlockDevices) == 0 {
		return nil, fmt.Errorf("no live iSCSI device found for volume %s", req.VolumeID)
	}
	expandDevice := h.expandDevice
	if expandDevice == nil {
		expandDevice = h.rescanExpandDevice
	}
	if err := expandDevice(req.DevicePath, req.BlockDevices); err != nil {
		return nil, err
	}

	// Raw block volumes hold whatever the workload wrote to them, commonly a
	// partition table. Device rescans are the whole job; filesystem tools must
	// not inspect or modify workload-owned block contents.
	if req.IsBlockVolume {
		h.log.V(LogLevelDebug).Info("Raw block volume, skipping filesystem resize", "volumeId", req.VolumeID, "device", req.DevicePath)
		return &ExpandResult{CapacityBytes: req.CapacityBytes}, nil
	}

	h.log.V(LogLevelDebug).Info("Resizing filesystem", "device", req.DevicePath, "volumePath", req.VolumePath)
	resized, err := h.resizer.Resize(req.DevicePath, req.VolumePath)
	if err != nil {
		return nil, fmt.Errorf("failed to resize filesystem: %w", err)
	}
	if resized {
		h.log.V(LogLevelDebug).Info("Filesystem resized successfully")
	}
	return &ExpandResult{CapacityBytes: req.CapacityBytes}, nil
}

// rescanSCSIHosts triggers a rescan of all SCSI hosts
func (h *ISCSIHandler) rescanSCSIHosts() {
	hostDir := "/sys/class/scsi_host"
	entries, err := os.ReadDir(hostDir)
	if err != nil {
		h.log.V(LogLevelTrace).Info("Failed to read SCSI hosts", "error", err)
		return
	}

	for _, entry := range entries {
		scanPath := filepath.Join(hostDir, entry.Name(), "scan")
		if err := os.WriteFile(scanPath, []byte("- - -"), 0o200); err != nil {
			h.log.V(LogLevelTrace).Info("Failed to scan SCSI host", "host", entry.Name(), "error", err)
		}
	}
}
