package driver

import (
	"context"
	"fmt"
	"os"
	"sync"
	"syscall"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"
	"k8s.io/utils/exec"
)

// NodeServer implements the CSI Node service
type NodeServer struct {
	driver        *Driver
	mounter       mount.Interface
	iscsiHandler  *ISCSIHandler
	nvmeofHandler *NVMeOFHandler
	nfsHandler    *NFSHandler
	volumeLocks   sync.Map // map[volumeID]*sync.Mutex - shared node lifecycle locks
	csi.UnimplementedNodeServer
}

// NodeServerConfig holds configuration for creating a new node server
type NodeServerConfig struct {
	Driver  *Driver
	Mounter mount.Interface
}

// NewNodeServer creates a new NodeServer with the provided configuration
func NewNodeServer(cfg *NodeServerConfig) (*NodeServer, error) {
	if cfg.Driver == nil {
		return nil, fmt.Errorf("driver is required")
	}

	mounter := cfg.Mounter
	if mounter == nil {
		mounter = mount.New("")
	}

	// Create SafeFormatAndMount for filesystem operations
	safeMounter := &mount.SafeFormatAndMount{
		Interface: mounter,
		Exec:      exec.New(),
	}

	iscsiHandler, err := NewISCSIHandler(safeMounter, cfg.Driver.Log())
	if err != nil {
		return nil, fmt.Errorf("failed to create iSCSI handler: %w", err)
	}

	nvmeofHandler, err := NewNVMeOFHandler(safeMounter, cfg.Driver.Log())
	if err != nil {
		return nil, fmt.Errorf("failed to create NVMe-oF handler: %w", err)
	}

	return &NodeServer{
		driver:        cfg.Driver,
		mounter:       mounter,
		iscsiHandler:  iscsiHandler,
		nvmeofHandler: nvmeofHandler,
		nfsHandler:    NewNFSHandler(mounter, cfg.Driver.Log()),
	}, nil
}

// TryAcquireLock attempts to acquire a per-volume lock without blocking.
// Stage, Publish, Unpublish, Unstage, and Expand all use the same volume ID so
// publication cannot race destructive teardown.
func (s *NodeServer) TryAcquireLock(key string) bool {
	mu, _ := s.volumeLocks.LoadOrStore(key, &sync.Mutex{})
	return mu.(*sync.Mutex).TryLock()
}

// ReleaseLock releases the lock for the given key
func (s *NodeServer) ReleaseLock(key string) {
	if mu, ok := s.volumeLocks.Load(key); ok {
		mu.(*sync.Mutex).Unlock()
	}
}

// getHandler returns the appropriate protocol handler for the request
func (s *NodeServer) getHandler(publishContext map[string]string) (ProtocolHandler, error) {
	switch publishContext[PublishContextProtocol] {
	case ProtocolISCSI:
		return s.iscsiHandler, nil
	case ProtocolNVMeOF:
		return s.nvmeofHandler, nil
	case ProtocolNFS:
		return s.nfsHandler, nil
	default:
		return nil, fmt.Errorf("unknown or missing protocol in publish context: %q", publishContext[PublishContextProtocol])
	}
}

// validateVolumeCapability checks if the requested capability is supported
func (s *NodeServer) validateVolumeCapability(cap *csi.VolumeCapability) error {
	if cap == nil {
		return fmt.Errorf("volume capability is nil")
	}

	// Must have either block or mount capability
	if cap.GetBlock() == nil && cap.GetMount() == nil {
		return fmt.Errorf("either block or mount volume capability is required")
	}

	// Check access mode is supported
	if cap.AccessMode == nil {
		return fmt.Errorf("access mode is required")
	}

	supportedModes := s.driver.VolumeCaps()
	for _, supported := range supportedModes {
		if cap.AccessMode.Mode == supported.Mode {
			return nil
		}
	}

	return fmt.Errorf("access mode %v not supported", cap.AccessMode.Mode)
}

// NodeStageVolume mounts the volume to the staging path (iSCSI) or is a no-op (NFS).
func (s *NodeServer) NodeStageVolume(ctx context.Context, req *csi.NodeStageVolumeRequest) (*csi.NodeStageVolumeResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodeStageVolume called", "volumeId", req.VolumeId, "stagingTargetPath", req.StagingTargetPath)

	// Validate request
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.StagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is required")
	}
	if req.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability is required")
	}

	// Validate volume capability is supported
	if err := s.validateVolumeCapability(req.VolumeCapability); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported volume capability: %v", err)
	}

	if !s.TryAcquireLock(req.VolumeId) {
		return nil, status.Errorf(codes.Aborted, "operation already in progress for volume %s", req.VolumeId)
	}
	defer s.ReleaseLock(req.VolumeId)

	// Get appropriate handler
	s.driver.Log().Info("NodeStageVolume received", "volumeId", req.VolumeId, "publishContext", req.PublishContext)
	handler, err := s.getHandler(req.PublishContext)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to determine protocol: %v", err)
	}

	// Raw block volumes are staged as a bind mount beneath the CSI staging
	// directory. Inspect both forms before connecting so an access-mode mismatch
	// cannot format over a raw volume or create an anchor inside a mounted filesystem.
	isBlockVolume := req.VolumeCapability.GetBlock() != nil
	stagedPath := req.StagingTargetPath
	if isBlockVolume {
		stagedPath = rawBlockStagingPath(req.StagingTargetPath)
	}
	mounts, err := s.mounter.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to inspect staging mounts: %v", err)
	}
	filesystemMount := effectiveMount(mounts, req.StagingTargetPath)
	blockMount := effectiveMount(mounts, rawBlockStagingPath(req.StagingTargetPath))
	if filesystemMount != nil && blockMount != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "both filesystem and raw-block staging mounts exist for volume %s", req.VolumeId)
	}
	if (isBlockVolume && filesystemMount != nil) || (!isBlockVolume && blockMount != nil) {
		return nil, status.Errorf(codes.FailedPrecondition, "existing staging mount has the wrong access type for volume %s", req.VolumeId)
	}

	entry := filesystemMount
	if isBlockVolume {
		entry = blockMount
	}
	if entry != nil {
		state, err := discoverMountedVolume(req.VolumeId, mounts, entry)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to validate existing staging mount: %v", err)
		}
		if state.Protocol != handler.Protocol() {
			return nil, status.Errorf(codes.FailedPrecondition, "staging path %s contains %s storage, expected %s", stagedPath, state.Protocol, handler.Protocol())
		}
		s.driver.Log().V(LogLevelDebug).Info("Volume already staged", "volumeId", req.VolumeId, "stagingPath", stagedPath)
		return &csi.NodeStageVolumeResponse{}, nil
	}

	notMounted, err := s.mounter.IsLikelyNotMountPoint(stagedPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal, "failed to check staging path: %v", err)
	}
	if !notMounted {
		return nil, status.Errorf(codes.Internal, "staging path %s is mounted but absent from the mount table", stagedPath)
	}

	// Extract filesystem type for mount volumes
	fsType := DefaultFSType
	var mountFlags []string
	if req.VolumeCapability.GetMount() != nil {
		if req.VolumeCapability.GetMount().FsType != "" {
			fsType = req.VolumeCapability.GetMount().FsType
		}
		mountFlags = req.VolumeCapability.GetMount().GetMountFlags()
	}

	// Build stage request
	stageReq := &StageRequest{
		VolumeID:         req.VolumeId,
		StagingPath:      req.StagingTargetPath,
		FSType:           fsType,
		MountFlags:       mountFlags,
		VolumeCapability: req.VolumeCapability,
		PublishContext:   req.PublishContext,
		VolumeContext:    req.VolumeContext,
		Secrets:          req.GetSecrets(),
		IsBlockVolume:    isBlockVolume,
	}

	// Stage volume
	if _, err := handler.Stage(ctx, stageReq); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to stage volume: %v", err)
	}

	s.driver.Log().V(LogLevelDebug).Info("Successfully staged volume", "volumeId", req.VolumeId, "stagingTargetPath", req.StagingTargetPath)
	return &csi.NodeStageVolumeResponse{}, nil
}

// NodeUnstageVolume unmounts and disconnects the volume from the staging path.
func (s *NodeServer) NodeUnstageVolume(ctx context.Context, req *csi.NodeUnstageVolumeRequest) (*csi.NodeUnstageVolumeResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodeUnstageVolume called", "volumeId", req.VolumeId, "stagingTargetPath", req.StagingTargetPath)

	// Validate request
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.StagingTargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "staging target path is required")
	}

	if !s.TryAcquireLock(req.VolumeId) {
		return nil, status.Errorf(codes.Aborted, "operation already in progress for volume %s", req.VolumeId)
	}
	defer s.ReleaseLock(req.VolumeId)

	// Capture transport identity before unmounting. If disconnect later fails, a
	// retry can recover the same target from its deterministic IQN/NQN suffix.
	state, err := s.discoverUnstageState(req.VolumeId, req.StagingTargetPath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to discover staged volume: %v", err)
	}

	if state.MountPath != "" {
		refs, err := s.mounter.GetMountRefs(state.MountPath)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to inspect publish references: %v", err)
		}
		if len(refs) > 0 {
			return nil, status.Errorf(codes.FailedPrecondition, "volume %s is still published at %v", req.VolumeId, refs)
		}
		if err := s.mounter.Unmount(state.MountPath); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to unmount staging path %s: %v", state.MountPath, err)
		}
		mounts, err := s.mounter.List()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to verify staging unmount: %v", err)
		}
		if effectiveMount(mounts, state.MountPath) != nil {
			return nil, status.Errorf(codes.Internal, "staging mount %s remains after unmount", state.MountPath)
		}
	}

	unstageReq := &UnstageRequest{
		VolumeID:    req.VolumeId,
		StagingPath: req.StagingTargetPath,
		MountPath:   state.MountPath,
		DevicePath:  state.DevicePath,
		ISCSI:       state.ISCSI,
		NVMeSubNQN:  state.NVMeSubNQN,
	}
	var handler ProtocolHandler
	switch state.Protocol {
	case ProtocolISCSI:
		handler = s.iscsiHandler
	case ProtocolNVMeOF:
		handler = s.nvmeofHandler
	case ProtocolNFS:
		handler = s.nfsHandler
	case "":
		// No staging mount and no matching live connection is idempotent success.
	default:
		return nil, status.Errorf(codes.Internal, "unsupported discovered protocol %q", state.Protocol)
	}
	if handler != nil {
		if err := handler.Unstage(ctx, unstageReq); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to unstage volume: %v", err)
		}
	}

	_ = os.Remove(rawBlockStagingPath(req.StagingTargetPath))
	_ = os.Remove(req.StagingTargetPath)

	mounts, err := s.mounter.List()
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to verify volume cleanup: %v", err)
	}
	if effectiveMount(mounts, req.StagingTargetPath) != nil || effectiveMount(mounts, rawBlockStagingPath(req.StagingTargetPath)) != nil {
		return nil, status.Errorf(codes.Internal, "staging mount remains for volume %s", req.VolumeId)
	}

	s.driver.Log().V(LogLevelDebug).Info("Successfully unstaged volume", "volumeId", req.VolumeId, "stagingTargetPath", req.StagingTargetPath)
	return &csi.NodeUnstageVolumeResponse{}, nil
}

// NodePublishVolume bind-mounts the staged volume to the target path.
func (s *NodeServer) NodePublishVolume(ctx context.Context, req *csi.NodePublishVolumeRequest) (*csi.NodePublishVolumeResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodePublishVolume called", "volumeId", req.VolumeId, "targetPath", req.TargetPath)

	// Validate request
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.TargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}
	if req.VolumeCapability == nil {
		return nil, status.Error(codes.InvalidArgument, "volume capability is required")
	}

	// Validate volume capability is supported
	if err := s.validateVolumeCapability(req.VolumeCapability); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "unsupported volume capability: %v", err)
	}

	// Check if this is a block volume request
	isBlockVolume := req.VolumeCapability.GetBlock() != nil

	if !s.TryAcquireLock(req.VolumeId) {
		return nil, status.Errorf(codes.Aborted, "operation already in progress for volume %s", req.VolumeId)
	}
	defer s.ReleaseLock(req.VolumeId)

	// Get the expected protocol before validating an idempotent publication.
	handler, err := s.getHandler(req.PublishContext)
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "failed to determine protocol: %v", err)
	}

	// The idempotency check belongs inside the volume-wide lock. A pre-existing
	// target file alone is not a published raw block volume; it must be mounted,
	// belong to this volume, and reference the expected staging mount.
	notMounted, err := s.mounter.IsLikelyNotMountPoint(req.TargetPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, status.Errorf(codes.Internal, "failed to check mount point: %v", err)
	}
	if !notMounted {
		mounts, err := s.mounter.List()
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to inspect existing publication: %v", err)
		}
		entry := effectiveMount(mounts, req.TargetPath)
		if entry == nil {
			return nil, status.Errorf(codes.Internal, "target path %s is mounted but absent from the mount table", req.TargetPath)
		}
		state, err := discoverMountedVolume(req.VolumeId, mounts, entry)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to validate existing publication: %v", err)
		}
		if state.Protocol != handler.Protocol() {
			return nil, status.Errorf(codes.FailedPrecondition, "target path %s contains %s storage, expected %s", req.TargetPath, state.Protocol, handler.Protocol())
		}
		if state.Protocol == ProtocolNFS {
			config := parseNFSConfig(req.PublishContext, req.VolumeContext)
			source, _, err := resolveMountSource(mounts, entry)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to inspect existing NFS publication: %v", err)
			}
			if source != formatNFSSource(config.Server, config.Path) {
				return nil, status.Errorf(codes.FailedPrecondition, "target path %s contains NFS source %s for another volume", req.TargetPath, source)
			}
		} else {
			stagedSource := req.StagingTargetPath
			if isBlockVolume {
				stagedSource = rawBlockStagingPath(req.StagingTargetPath)
			}
			refs, err := s.mounter.GetMountRefs(stagedSource)
			if err != nil {
				return nil, status.Errorf(codes.Internal, "failed to inspect staging references: %v", err)
			}
			if !containsCleanPath(refs, req.TargetPath) {
				return nil, status.Errorf(codes.FailedPrecondition, "target path %s is not published from staging path %s", req.TargetPath, stagedSource)
			}
		}
		s.driver.Log().V(LogLevelDebug).Info("Volume already published", "volumeId", req.VolumeId, "targetPath", req.TargetPath)
		return &csi.NodePublishVolumeResponse{}, nil
	}

	// Extract mount options
	fsType := ""
	var mountFlags []string
	if req.VolumeCapability.GetMount() != nil {
		fsType = req.VolumeCapability.GetMount().FsType
		mountFlags = req.VolumeCapability.GetMount().GetMountFlags()
	}

	// Build publish request
	publishReq := &PublishRequest{
		VolumeID:         req.VolumeId,
		StagingPath:      req.StagingTargetPath,
		TargetPath:       req.TargetPath,
		FSType:           fsType,
		MountFlags:       mountFlags,
		ReadOnly:         req.Readonly,
		VolumeCapability: req.VolumeCapability,
		PublishContext:   req.PublishContext,
		VolumeContext:    req.VolumeContext,
		IsBlockVolume:    isBlockVolume,
	}

	// Publish volume
	if err := handler.Publish(ctx, publishReq); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to publish volume: %v", err)
	}

	s.driver.Log().V(LogLevelDebug).Info("Successfully published volume", "volumeId", req.VolumeId, "targetPath", req.TargetPath)
	return &csi.NodePublishVolumeResponse{}, nil
}

// NodeUnpublishVolume unmounts the volume from the target path.
func (s *NodeServer) NodeUnpublishVolume(ctx context.Context, req *csi.NodeUnpublishVolumeRequest) (*csi.NodeUnpublishVolumeResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodeUnpublishVolume called", "volumeId", req.VolumeId, "targetPath", req.TargetPath)

	// Validate request
	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.TargetPath == "" {
		return nil, status.Error(codes.InvalidArgument, "target path is required")
	}

	if !s.TryAcquireLock(req.VolumeId) {
		return nil, status.Errorf(codes.Aborted, "operation already in progress for volume %s", req.VolumeId)
	}
	defer s.ReleaseLock(req.VolumeId)

	// CleanupMountPoint handles: check if mounted, unmount, remove path (idempotent)
	if err := mount.CleanupMountPoint(req.TargetPath, s.mounter, true); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to cleanup mount point: %v", err)
	}

	s.driver.Log().V(LogLevelDebug).Info("Successfully unpublished volume", "volumeId", req.VolumeId, "targetPath", req.TargetPath)
	return &csi.NodeUnpublishVolumeResponse{}, nil
}

// NodeGetInfo returns the node ID.
func (s *NodeServer) NodeGetInfo(ctx context.Context, req *csi.NodeGetInfoRequest) (*csi.NodeGetInfoResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodeGetInfo called")

	return &csi.NodeGetInfoResponse{
		NodeId: s.driver.NodeID(),
		// MaxVolumesPerNode: 0 means no limit
	}, nil
}

// NodeGetCapabilities returns the capabilities of the node service.
func (s *NodeServer) NodeGetCapabilities(ctx context.Context, req *csi.NodeGetCapabilitiesRequest) (*csi.NodeGetCapabilitiesResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodeGetCapabilities called")

	return &csi.NodeGetCapabilitiesResponse{
		Capabilities: s.driver.NodeCaps(),
	}, nil
}

// NodeGetVolumeStats returns capacity statistics for a mounted volume.
func (s *NodeServer) NodeGetVolumeStats(ctx context.Context, req *csi.NodeGetVolumeStatsRequest) (*csi.NodeGetVolumeStatsResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodeGetVolumeStats called", "volumeId", req.VolumeId, "volumePath", req.VolumePath)

	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.VolumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volume path is required")
	}

	_, err := os.Stat(req.VolumePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, status.Errorf(codes.NotFound, "volume path %s does not exist", req.VolumePath)
		}
		return nil, status.Errorf(codes.Internal, "failed to stat volume path: %v", err)
	}

	stats, err := s.getFSStats(req.VolumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to get filesystem stats: %v", err)
	}

	return &csi.NodeGetVolumeStatsResponse{
		Usage: []*csi.VolumeUsage{
			{
				Unit:      csi.VolumeUsage_BYTES,
				Available: stats.availableBytes,
				Total:     stats.totalBytes,
				Used:      stats.usedBytes,
			},
			{
				Unit:      csi.VolumeUsage_INODES,
				Available: stats.availableInodes,
				Total:     stats.totalInodes,
				Used:      stats.usedInodes,
			},
		},
	}, nil
}

// NodeExpandVolume expands the filesystem on iSCSI volumes after controller expansion.
func (s *NodeServer) NodeExpandVolume(ctx context.Context, req *csi.NodeExpandVolumeRequest) (*csi.NodeExpandVolumeResponse, error) {
	s.driver.Log().V(LogLevelDebug).Info("NodeExpandVolume called", "volumeId", req.VolumeId, "volumePath", req.VolumePath)

	if req.VolumeId == "" {
		return nil, status.Error(codes.InvalidArgument, "volume ID is required")
	}
	if req.VolumePath == "" {
		return nil, status.Error(codes.InvalidArgument, "volume path is required")
	}

	// Verify volume path exists
	if _, err := os.Stat(req.VolumePath); os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "volume path %s does not exist", req.VolumePath)
	}

	if !s.TryAcquireLock(req.VolumeId) {
		return nil, status.Errorf(codes.Aborted, "operation already in progress for volume %s", req.VolumeId)
	}
	defer s.ReleaseLock(req.VolumeId)

	// Determine capacity
	var capacityBytes int64
	if req.CapacityRange != nil {
		capacityBytes = req.CapacityRange.RequiredBytes
	}

	state, err := s.discoverExpandState(req.VolumeId, req.StagingTargetPath, req.VolumePath)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to discover volume device: %v", err)
	}
	var handler ProtocolHandler
	switch state.Protocol {
	case ProtocolISCSI:
		handler = s.iscsiHandler
	case ProtocolNVMeOF:
		handler = s.nvmeofHandler
	case ProtocolNFS:
		handler = s.nfsHandler
	case "":
		return nil, status.Errorf(codes.NotFound, "no live mount or transport connection found for volume %s", req.VolumeId)
	default:
		return nil, status.Errorf(codes.Internal, "unsupported discovered protocol %q", state.Protocol)
	}

	expandReq := &ExpandRequest{
		VolumeID:        req.VolumeId,
		VolumePath:      req.VolumePath,
		DevicePath:      state.DevicePath,
		BlockDevices:    state.BlockDevices,
		NVMeControllers: state.NVMeControllers,
		CapacityBytes:   capacityBytes,
		IsBlockVolume:   isBlockVolumeExpansion(req.VolumeCapability, req.VolumePath),
	}

	// Expand volume
	result, err := handler.Expand(ctx, expandReq)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to expand volume: %v", err)
	}

	return &csi.NodeExpandVolumeResponse{CapacityBytes: result.CapacityBytes}, nil
}

// isBlockVolumeExpansion reports whether the volume being expanded is a raw
// block volume, which has no filesystem for the node to grow.
//
// The volume capability is optional on this RPC and cannot be relied on: a CO
// may send only an access mode, or attach a mount access type whenever the
// PersistentVolume names a filesystem, so an absent block access type does not
// mean the volume has a filesystem. The volume path is the authoritative
// signal, because block volumes are published as the device itself while
// filesystem volumes are published as a directory.
func isBlockVolumeExpansion(capability *csi.VolumeCapability, path string) bool {
	if capability.GetBlock() != nil {
		return true
	}

	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

type fsStats struct {
	totalBytes      int64
	availableBytes  int64
	usedBytes       int64
	totalInodes     int64
	availableInodes int64
	usedInodes      int64
}

// getFSStats retrieves filesystem statistics using statfs.
func (s *NodeServer) getFSStats(path string) (*fsStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, fmt.Errorf("statfs failed: %v", err)
	}

	blockSize := stat.Frsize
	if blockSize == 0 {
		blockSize = stat.Bsize
	}

	totalBytes := int64(stat.Blocks) * blockSize
	availableBytes := int64(stat.Bavail) * blockSize
	freeBytes := int64(stat.Bfree) * blockSize
	usedBytes := totalBytes - freeBytes

	totalInodes := int64(stat.Files)
	availableInodes := int64(stat.Ffree)
	usedInodes := totalInodes - availableInodes

	return &fsStats{
		totalBytes:      totalBytes,
		availableBytes:  availableBytes,
		usedBytes:       usedBytes,
		totalInodes:     totalInodes,
		availableInodes: availableInodes,
		usedInodes:      usedInodes,
	}, nil
}
