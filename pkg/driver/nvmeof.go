package driver

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/mount-utils"
)

// nvmeExecCommand runs nvme-cli / modprobe; overridable in tests.
var nvmeExecCommand = exec.CommandContext

const (
	// nvmeCtrlLossTmo keeps the connection retrying through transient target
	// outages instead of failing the mount on a brief blip (pattern from ceph-csi).
	nvmeCtrlLossTmo = "1800"

	// nvmeByIDPrefix is the udev by-id symlink prefix for a namespace UUID.
	nvmeByIDPrefix = "/dev/disk/by-id/nvme-uuid."

	nvmeDeviceWaitAttempts = 30
	nvmeDeviceWaitInterval = 200 * time.Millisecond
)

// NVMeOFHandler implements the ProtocolHandler interface for NVMe-oF/TCP volumes.
type NVMeOFHandler struct {
	mounter *mount.SafeFormatAndMount
	resizer *mount.ResizeFs
	log     logr.Logger
}

// NVMeOFConfig holds NVMe-oF configuration parsed from publish/volume contexts.
type NVMeOFConfig struct {
	SubNQN        string
	PortAddr      string
	PortSvcID     string
	Transport     string
	NamespaceUUID string
	HostNQN       string
	DHCHAPKey     string
	DHCHAPCtrlKey string
}

// NewNVMeOFHandler creates a new NVMe-oF protocol handler.
func NewNVMeOFHandler(mounter *mount.SafeFormatAndMount, log logr.Logger) (*NVMeOFHandler, error) {
	return &NVMeOFHandler{
		mounter: mounter,
		resizer: mount.NewResizeFs(mounter.Exec),
		log:     log,
	}, nil
}

// Protocol returns the protocol name.
func (h *NVMeOFHandler) Protocol() string {
	return ProtocolNVMeOF
}

// parseNVMeOFConfig extracts NVMe-oF configuration. Connection parameters come from
// the publish context; DH-CHAP credentials come from the node-stage secret when
// present, falling back to the volume context (StorageClass params) for backward
// compatibility, mirroring how iSCSI CHAP is delivered.
func parseNVMeOFConfig(publishContext, volumeContext, secrets map[string]string) *NVMeOFConfig {
	transport := publishContext[PublishContextNVMeTransport]
	if transport == "" {
		transport = defaultNVMeOFTransport
	}
	return &NVMeOFConfig{
		SubNQN:        publishContext[PublishContextNVMeSubNQN],
		PortAddr:      publishContext[PublishContextNVMePortAddr],
		PortSvcID:     publishContext[PublishContextNVMePortSvcID],
		Transport:     transport,
		NamespaceUUID: publishContext[PublishContextNVMeNSUUID],
		HostNQN:       volumeContext[paramNVMeOFHostNQN],
		DHCHAPKey:     secretOrParam(secrets, volumeContext, paramNVMeOFDHCHAPKey),
		DHCHAPCtrlKey: secretOrParam(secrets, volumeContext, paramNVMeOFDHCHAPCtrlKey),
	}
}

// Stage connects the NVMe-oF subsystem and prepares the device (format+mount for
// filesystem volumes, or returns the device path for block volumes).
func (h *NVMeOFHandler) Stage(ctx context.Context, req *StageRequest) (*StageResult, error) {
	h.log.V(LogLevelDebug).Info("NVMe-oF Stage", "volumeId", req.VolumeID, "stagingPath", req.StagingPath, "isBlock", req.IsBlockVolume)

	config := parseNVMeOFConfig(req.PublishContext, req.VolumeContext, req.Secrets)
	if config.SubNQN == "" || config.PortAddr == "" || config.PortSvcID == "" {
		return nil, fmt.Errorf("NVMe-oF subsystem NQN and portal are required (check controller publish context)")
	}

	if err := h.loadKernelModules(ctx); err != nil {
		h.log.V(LogLevelTrace).Info("Failed to load NVMe kernel modules (may already be loaded)", "error", err)
	}

	devicePath, err := h.connectAndDiscover(ctx, config)
	if err != nil {
		return nil, err
	}
	h.log.V(LogLevelDebug).Info("NVMe-oF connected", "device", devicePath, "subnqn", config.SubNQN)

	// Raw block volumes get the same durable staging anchor used by iSCSI.
	if req.IsBlockVolume {
		if err := stageBlockDevice(h.mounter, devicePath, req.StagingPath); err != nil {
			return nil, err
		}
		return &StageResult{DevicePath: devicePath}, nil
	}

	fsType := req.FSType
	if fsType == "" {
		fsType = DefaultFSType
	}

	if err := os.MkdirAll(req.StagingPath, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create staging directory: %w", err)
	}

	// For XFS, add nouuid so a cloned volume sharing the source UUID can mount.
	mountFlags := req.MountFlags
	if fsType == fsTypeXFS && !slices.Contains(mountFlags, mountOptionNouuid) {
		mountFlags = append(mountFlags, mountOptionNouuid)
	}

	if err := h.mounter.FormatAndMount(devicePath, req.StagingPath, fsType, mountFlags); err != nil {
		return nil, fmt.Errorf("failed to format and mount device: %w", err)
	}

	// Resize the filesystem if the device is larger (e.g. snapshot restored to a
	// larger PVC); FormatAndMount skips formatting when a filesystem already exists.
	if needsResize, err := h.resizer.NeedResize(devicePath, req.StagingPath); err == nil && needsResize {
		if _, err := h.resizer.Resize(devicePath, req.StagingPath); err != nil {
			return nil, fmt.Errorf("failed to resize filesystem after mount: %w", err)
		}
	}

	h.log.V(LogLevelDebug).Info("NVMe-oF volume staged", "volumeId", req.VolumeID, "stagingPath", req.StagingPath)
	return &StageResult{DevicePath: devicePath}, nil
}

// connectAndDiscover connects (idempotently) and resolves the namespace block device.
func (h *NVMeOFHandler) connectAndDiscover(ctx context.Context, config *NVMeOFConfig) (string, error) {
	// Already connected? Reuse the device (idempotency).
	if dev := h.findDevice(config); dev != "" {
		return dev, nil
	}

	if err := h.nvmeConnect(ctx, config); err != nil {
		// Tolerate "already connected" races: re-check for the device.
		if dev := h.waitForDevice(ctx, config); dev != "" {
			return dev, nil
		}
		return "", fmt.Errorf("failed to connect to NVMe-oF subsystem %s at %s:%s: %w", config.SubNQN, config.PortAddr, config.PortSvcID, err)
	}

	dev := h.waitForDevice(ctx, config)
	if dev == "" {
		// Roll back the half-open connection so we don't leak controllers.
		_ = h.nvmeDisconnect(ctx, config.SubNQN)
		return "", fmt.Errorf("NVMe-oF device for %s did not appear after connect", config.SubNQN)
	}
	return dev, nil
}

// nvmeConnect runs `nvme connect` with optional hostNQN and DH-CHAP secrets.
func (h *NVMeOFHandler) nvmeConnect(ctx context.Context, config *NVMeOFConfig) error {
	args := []string{
		"connect",
		"-t", config.Transport,
		"-n", config.SubNQN,
		"-a", config.PortAddr,
		"-s", config.PortSvcID,
		"-l", nvmeCtrlLossTmo,
	}
	if config.HostNQN != "" {
		args = append(args, "--hostnqn", config.HostNQN)
	}
	if config.DHCHAPKey != "" {
		args = append(args, "--dhchap-secret", config.DHCHAPKey)
	}
	if config.DHCHAPCtrlKey != "" {
		args = append(args, "--dhchap-ctrl-secret", config.DHCHAPCtrlKey)
	}

	out, err := nvmeExecCommand(ctx, "nvme", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nvme connect failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// nvmeDisconnect disconnects all controllers for a subsystem NQN. Safe because the
// driver uses one subsystem per volume (one namespace per subsystem).
func (h *NVMeOFHandler) nvmeDisconnect(ctx context.Context, subnqn string) error {
	out, err := nvmeExecCommand(ctx, "nvme", "disconnect", "-n", subnqn).CombinedOutput()
	if err != nil {
		return fmt.Errorf("nvme disconnect failed: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// nvmeRescanNamespace triggers a controller-side namespace rescan to pick up a new
// size after online expansion. ns-rescan targets the controller char device.
func (h *NVMeOFHandler) nvmeRescanNamespace(ctx context.Context, devicePath string) error {
	ctrl := nvmeControllerForDevice(devicePath)
	if ctrl == "" {
		return fmt.Errorf("could not derive controller device from %s", devicePath)
	}
	return h.nvmeRescanControllers(ctx, []string{ctrl})
}

func (h *NVMeOFHandler) nvmeRescanControllers(ctx context.Context, controllers []string) error {
	for _, controller := range controllers {
		out, err := nvmeExecCommand(ctx, "nvme", "ns-rescan", controller).CombinedOutput()
		if err != nil {
			return fmt.Errorf("nvme ns-rescan %s failed: %w (output: %s)", controller, err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

// loadKernelModules ensures the NVMe/TCP fabrics modules are loaded (best-effort).
func (h *NVMeOFHandler) loadKernelModules(ctx context.Context) error {
	var firstErr error
	for _, mod := range []string{"nvme_tcp", "nvme_fabrics"} {
		if out, err := nvmeExecCommand(ctx, "modprobe", mod).CombinedOutput(); err != nil {
			h.log.V(LogLevelTrace).Info("modprobe failed", "module", mod, "output", strings.TrimSpace(string(out)))
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}

// findDevice resolves the namespace block device without waiting: first via the
// by-id UUID symlink, then via a sysfs subsysnqn match.
func (h *NVMeOFHandler) findDevice(config *NVMeOFConfig) string {
	if config.NamespaceUUID != "" {
		for _, cand := range nvmeByIDCandidates(config.NamespaceUUID) {
			if resolved, err := filepath.EvalSymlinks(cand); err == nil {
				return resolved
			}
		}
	}
	return nvmeDeviceBySubsysNQN(config.SubNQN)
}

// waitForDevice polls findDevice with backoff until the device appears or timeout.
func (h *NVMeOFHandler) waitForDevice(ctx context.Context, config *NVMeOFConfig) string {
	for i := 0; i < nvmeDeviceWaitAttempts; i++ {
		if dev := h.findDevice(config); dev != "" {
			return dev
		}
		select {
		case <-ctx.Done():
			return ""
		case <-time.After(nvmeDeviceWaitInterval):
		}
	}
	return ""
}

// nvmeByIDCandidates returns candidate by-id symlink paths for a namespace UUID,
// trying the value as-is and lowercased.
func nvmeByIDCandidates(uuid string) []string {
	candidates := []string{nvmeByIDPrefix + uuid}
	if lower := strings.ToLower(uuid); lower != uuid {
		candidates = append(candidates, nvmeByIDPrefix+lower)
	}
	return candidates
}

// nvmeDeviceBySubsysNQN scans sysfs for a controller whose subsysnqn matches and
// returns its namespace block device (e.g. /dev/nvme0n1).
func nvmeDeviceBySubsysNQN(subnqn string) string {
	if subnqn == "" {
		return ""
	}
	entries, err := os.ReadDir(sysClassNVMeDir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		ctrl := e.Name() // e.g. nvme0
		data, err := os.ReadFile(filepath.Join(sysClassNVMeDir, ctrl, "subsysnqn"))
		if err != nil || strings.TrimSpace(string(data)) != subnqn {
			continue
		}
		nsEntries, err := os.ReadDir(filepath.Join(sysClassNVMeDir, ctrl))
		if err != nil {
			continue
		}
		for _, ns := range nsEntries {
			name := ns.Name()
			if strings.HasPrefix(name, ctrl+"n") {
				devPath := "/dev/" + name
				if _, err := os.Stat(devPath); err == nil {
					return devPath
				}
			}
		}
	}
	return ""
}

// nvmeControllerForDevice maps a namespace block device to its controller char
// device, e.g. "/dev/nvme0n1" -> "/dev/nvme0". Returns "" if the path is not of
// the expected "/dev/nvme<ctrl>n<nsid>" form.
func nvmeControllerForDevice(devicePath string) string {
	const prefix = "/dev/nvme"
	if !strings.HasPrefix(devicePath, prefix) {
		return ""
	}
	rest := devicePath[len(prefix):] // e.g. "0n1"
	sep := strings.IndexByte(rest, 'n')
	if sep <= 0 {
		return ""
	}
	ctrlNum := rest[:sep] // "0"
	nsNum := rest[sep+1:] // "1"
	if !isAllDigits(ctrlNum) || !isAllDigits(nsNum) {
		return ""
	}
	return prefix + ctrlNum
}

// isAllDigits reports whether s is non-empty and consists only of ASCII digits.
func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Unstage disconnects the live subsystem discovered by NodeUnstageVolume. The
// staging mount is removed centrally before this runs, and a retry can recover the
// same subsystem from its deterministic NQN suffix.
func (h *NVMeOFHandler) Unstage(ctx context.Context, req *UnstageRequest) error {
	h.log.V(LogLevelDebug).Info("NVMe-oF Unstage", "volumeId", req.VolumeID, "subnqn", req.NVMeSubNQN)
	if req.NVMeSubNQN == "" {
		return fmt.Errorf("missing live NVMe-oF subsystem identity for volume %s", req.VolumeID)
	}
	connected, err := nvmeSubsystemConnected(req.NVMeSubNQN)
	if err != nil {
		return fmt.Errorf("failed to inspect NVMe-oF subsystem %s: %w", req.NVMeSubNQN, err)
	}
	if connected {
		if err := h.nvmeDisconnect(ctx, req.NVMeSubNQN); err != nil {
			return fmt.Errorf("failed to disconnect NVMe-oF subsystem %s: %w", req.NVMeSubNQN, err)
		}
	}
	connected, err = nvmeSubsystemConnected(req.NVMeSubNQN)
	if err != nil {
		return fmt.Errorf("failed to verify NVMe-oF disconnect for %s: %w", req.NVMeSubNQN, err)
	}
	if connected {
		return fmt.Errorf("NVMe-oF subsystem %s remains connected after disconnect", req.NVMeSubNQN)
	}
	return nil
}

// Publish bind-mounts the staged volume (or block device) to the target path.
func (h *NVMeOFHandler) Publish(ctx context.Context, req *PublishRequest) error {
	h.log.V(LogLevelDebug).Info("NVMe-oF Publish", "volumeId", req.VolumeID, "targetPath", req.TargetPath, "isBlock", req.IsBlockVolume)

	if req.IsBlockVolume {
		return h.publishBlockVolume(req)
	}

	if req.StagingPath == "" {
		return fmt.Errorf("staging path is required for NVMe-oF mount volumes")
	}

	notMounted, err := h.mounter.IsLikelyNotMountPoint(req.StagingPath)
	if err != nil || notMounted {
		return fmt.Errorf("volume not staged at %s", req.StagingPath)
	}

	if err := os.MkdirAll(req.TargetPath, 0o750); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	mountOptions := []string{mountOptionBind}
	if req.ReadOnly {
		mountOptions = append(mountOptions, mountOptionReadOnly)
	}
	if err := h.mounter.Mount(req.StagingPath, req.TargetPath, "", mountOptions); err != nil {
		return fmt.Errorf("failed to bind mount: %w", err)
	}
	return nil
}

// publishBlockVolume bind-mounts the staged raw-block anchor to the workload.
func (h *NVMeOFHandler) publishBlockVolume(req *PublishRequest) error {
	return publishBlockDevice(h.mounter, req.StagingPath, req.TargetPath, req.ReadOnly)
}

// Unpublish unmounts the target path.
func (h *NVMeOFHandler) Unpublish(ctx context.Context, req *UnpublishRequest) error {
	h.log.V(LogLevelDebug).Info("NVMe-oF Unpublish", "volumeId", req.VolumeID, "targetPath", req.TargetPath)

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
	return nil
}

// Expand rescans every live controller for the namespace and grows the filesystem.
func (h *NVMeOFHandler) Expand(ctx context.Context, req *ExpandRequest) (*ExpandResult, error) {
	h.log.V(LogLevelDebug).Info("NVMe-oF Expand", "volumeId", req.VolumeID, "volumePath", req.VolumePath)
	if req.DevicePath == "" || len(req.NVMeControllers) == 0 {
		return nil, fmt.Errorf("no live NVMe-oF device found for volume %s", req.VolumeID)
	}
	if err := h.nvmeRescanControllers(ctx, req.NVMeControllers); err != nil {
		return nil, err
	}

	// Raw block volumes have no filesystem the node may grow; controller rescans
	// are all the expansion they need.
	if req.IsBlockVolume {
		h.log.V(LogLevelDebug).Info("Raw block volume, skipping filesystem resize", "volumeId", req.VolumeID, "device", req.DevicePath)
		return &ExpandResult{CapacityBytes: req.CapacityBytes}, nil
	}

	if _, err := h.resizer.Resize(req.DevicePath, req.VolumePath); err != nil {
		return nil, fmt.Errorf("failed to resize filesystem: %w", err)
	}
	return &ExpandResult{CapacityBytes: req.CapacityBytes}, nil
}
