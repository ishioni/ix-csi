package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	iscsilib "github.com/kubernetes-csi/csi-lib-iscsi/iscsi"
	"k8s.io/mount-utils"
)

const rawBlockStagingFilename = "block_device"

var (
	sysClassBlockDir  = "/sys/class/block"
	sysClassNVMeDir   = "/sys/class/nvme"
	sysDevBlockDir    = "/sys/dev/block"
	iscsiNodeDBDir    = "/var/lib/iscsi/nodes"
	deviceDir         = "/dev"
	evalDeviceSymlink = filepath.EvalSymlinks
	iscsiGetSessions  = iscsilib.GetSessions
)

type volumeState struct {
	Protocol        string
	MountPath       string
	DevicePath      string
	BlockDevices    []string
	ISCSI           *ISCSIConnection
	NVMeSubNQN      string
	NVMeControllers []string
}

type iscsiSession struct {
	ID     int
	Portal string
	IQN    string
}

type nvmeController struct {
	Name      string
	SubNQN    string
	Transport string
}

func rawBlockStagingPath(stagingPath string) string {
	return filepath.Join(stagingPath, rawBlockStagingFilename)
}

func stageBlockDevice(mounter mount.Interface, devicePath, stagingPath string) error {
	if devicePath == "" {
		return fmt.Errorf("block device path is empty")
	}
	if stagingPath == "" {
		return fmt.Errorf("staging path is empty")
	}

	if err := os.MkdirAll(stagingPath, 0o750); err != nil {
		return fmt.Errorf("failed to create block staging directory: %w", err)
	}
	anchor := rawBlockStagingPath(stagingPath)
	file, err := os.OpenFile(anchor, os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return fmt.Errorf("failed to create block staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close block staging file: %w", err)
	}

	notMounted, err := mounter.IsLikelyNotMountPoint(anchor)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to inspect block staging file: %w", err)
	}
	if !notMounted {
		return nil
	}
	if err := mounter.Mount(devicePath, anchor, "", []string{mountOptionBind}); err != nil {
		_ = os.Remove(anchor)
		return fmt.Errorf("failed to bind mount block device at staging path: %w", err)
	}
	return nil
}

func publishBlockDevice(mounter mount.Interface, stagingPath, targetPath string, readOnly bool) error {
	anchor := rawBlockStagingPath(stagingPath)
	notMounted, err := mounter.IsLikelyNotMountPoint(anchor)
	if err != nil || notMounted {
		return fmt.Errorf("block volume is not staged at %s", anchor)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0o750); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}
	file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_RDWR, 0o660)
	if err != nil {
		return fmt.Errorf("failed to create target file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("failed to close target file: %w", err)
	}

	options := []string{mountOptionBind}
	if readOnly {
		options = append(options, mountOptionReadOnly)
	}
	if err := mounter.Mount(anchor, targetPath, "", options); err != nil {
		_ = os.Remove(targetPath)
		return fmt.Errorf("failed to bind mount staged block device: %w", err)
	}
	return nil
}

func effectiveMount(mounts []mount.MountPoint, path string) *mount.MountPoint {
	if path == "" {
		return nil
	}
	target := filepath.Clean(path)
	var found *mount.MountPoint
	for i := range mounts {
		if filepath.Clean(mounts[i].Path) == target {
			entry := mounts[i]
			found = &entry
		}
	}
	return found
}

func resolveMountSource(mounts []mount.MountPoint, entry *mount.MountPoint) (string, string, error) {
	if entry == nil {
		return "", "", fmt.Errorf("mount entry is nil")
	}

	source := entry.Device
	fsType := entry.Type
	seen := map[string]bool{filepath.Clean(entry.Path): true}
	for filepath.IsAbs(source) {
		cleanSource := filepath.Clean(source)
		if seen[cleanSource] {
			return "", "", fmt.Errorf("mount source cycle at %s", cleanSource)
		}
		parent := effectiveMount(mounts, cleanSource)
		if parent == nil {
			break
		}
		seen[cleanSource] = true
		source = parent.Device
		if parent.Type != "" {
			fsType = parent.Type
		}
	}
	return source, fsType, nil
}

func resolveBlockDevices(devicePath string) (string, []string, error) {
	resolved, err := evalDeviceSymlink(devicePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to resolve block device %s: %w", devicePath, err)
	}
	rel, err := filepath.Rel(deviceDir, resolved)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", nil, fmt.Errorf("mount source %s does not resolve beneath %s", devicePath, deviceDir)
	}
	name := filepath.Base(resolved)
	if name == "." || name == string(filepath.Separator) || name == "" {
		return "", nil, fmt.Errorf("invalid block device path %s", resolved)
	}

	var leaves []string
	visiting := make(map[string]bool)
	var walk func(string) error
	walk = func(blockName string) error {
		if visiting[blockName] {
			return fmt.Errorf("block-device slave cycle at %s", blockName)
		}
		visiting[blockName] = true
		defer delete(visiting, blockName)

		slavesDir := filepath.Join(sysClassBlockDir, blockName, "slaves")
		entries, err := os.ReadDir(slavesDir)
		if err != nil {
			return fmt.Errorf("failed to inspect slaves for %s: %w", blockName, err)
		}
		if len(entries) == 0 {
			leaves = append(leaves, filepath.Join(deviceDir, blockName))
			return nil
		}
		for _, slave := range entries {
			if err := walk(slave.Name()); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(name); err != nil {
		return "", nil, err
	}
	sort.Strings(leaves)
	leaves = compactStrings(leaves)
	return resolved, leaves, nil
}

func parseISCSISessions(output string) ([]iscsiSession, error) {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil, nil
	}

	var sessions []iscsiSession
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return nil, fmt.Errorf("cannot parse iscsiadm session line %q", line)
		}
		idText := strings.Trim(fields[1], "[]")
		id, err := strconv.Atoi(idText)
		if err != nil {
			return nil, fmt.Errorf("cannot parse iSCSI session ID %q: %w", fields[1], err)
		}
		portal := fields[2]
		if comma := strings.LastIndexByte(portal, ','); comma >= 0 {
			portal = portal[:comma]
		}
		if portal == "" || fields[3] == "" {
			return nil, fmt.Errorf("incomplete iscsiadm session line %q", line)
		}
		sessions = append(sessions, iscsiSession{ID: id, Portal: portal, IQN: fields[3]})
	}
	return sessions, nil
}

func currentISCSISessions() ([]iscsiSession, error) {
	output, err := iscsiGetSessions()
	if err != nil {
		if isNoISCSIObjectsFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list iSCSI sessions: %w", withCommandOutput(err))
	}
	return parseISCSISessions(output)
}

func iscsiSessionIDForBlockDevice(devicePath string) (int, bool, error) {
	link := filepath.Join(sysClassBlockDir, filepath.Base(devicePath), "device")
	target, err := filepath.EvalSymlinks(link)
	if err != nil {
		return 0, false, fmt.Errorf("failed to inspect transport for %s: %w", devicePath, err)
	}
	for _, component := range strings.Split(filepath.ToSlash(target), "/") {
		if !strings.HasPrefix(component, "session") {
			continue
		}
		idText := strings.TrimPrefix(component, "session")
		id, err := strconv.Atoi(idText)
		if err == nil {
			return id, true, nil
		}
	}
	return 0, false, nil
}

func iscsiConnectionForDevices(volumeID string, devices []string) (*ISCSIConnection, bool, error) {
	sessionIDs := make(map[int]bool)
	for _, device := range devices {
		id, found, err := iscsiSessionIDForBlockDevice(device)
		if err != nil {
			return nil, false, err
		}
		if !found {
			return nil, false, nil
		}
		sessionIDs[id] = true
	}
	if len(sessionIDs) == 0 {
		return nil, false, nil
	}

	sessions, err := currentISCSISessions()
	if err != nil {
		return nil, false, err
	}
	expectedSuffix := ":" + makeISCSITargetSuffix(volumeID)
	targetIQN := ""
	var portals []string
	foundIDs := make(map[int]bool)
	for _, session := range sessions {
		if !sessionIDs[session.ID] {
			continue
		}
		foundIDs[session.ID] = true
		if !strings.HasSuffix(session.IQN, expectedSuffix) {
			return nil, false, fmt.Errorf("iSCSI target %q for mounted device does not belong to volume %q", session.IQN, volumeID)
		}
		if targetIQN != "" && targetIQN != session.IQN {
			return nil, false, fmt.Errorf("mounted devices map to multiple iSCSI targets: %q and %q", targetIQN, session.IQN)
		}
		targetIQN = session.IQN
		portals = append(portals, session.Portal)
	}
	if len(foundIDs) != len(sessionIDs) {
		return nil, false, fmt.Errorf("could not map every mounted iSCSI device to a live session")
	}
	sort.Strings(portals)
	return &ISCSIConnection{TargetIQN: targetIQN, Portals: compactStrings(portals)}, true, nil
}

func findISCSIConnectionByVolumeID(volumeID string) (*ISCSIConnection, error) {
	sessions, err := currentISCSISessions()
	if err != nil {
		return nil, err
	}
	expectedSuffix := ":" + makeISCSITargetSuffix(volumeID)
	byTarget := make(map[string][]string)
	for _, session := range sessions {
		if strings.HasSuffix(session.IQN, expectedSuffix) {
			byTarget[session.IQN] = append(byTarget[session.IQN], session.Portal)
		}
	}
	if len(byTarget) == 0 {
		return findISCSINodeRecordByVolumeID(volumeID)
	}
	if len(byTarget) > 1 {
		return nil, fmt.Errorf("multiple iSCSI targets match volume %q", volumeID)
	}
	for iqn, portals := range byTarget {
		sort.Strings(portals)
		return &ISCSIConnection{TargetIQN: iqn, Portals: compactStrings(portals)}, nil
	}
	return nil, nil
}

func findISCSINodeRecordByVolumeID(volumeID string) (*ISCSIConnection, error) {
	entries, err := os.ReadDir(iscsiNodeDBDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to inspect iSCSI node database: %w", err)
	}
	expectedSuffix := ":" + makeISCSITargetSuffix(volumeID)
	var matches []string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), expectedSuffix) {
			matches = append(matches, entry.Name())
		}
	}
	if len(matches) == 0 {
		return nil, nil
	}
	if len(matches) > 1 {
		return nil, fmt.Errorf("multiple iSCSI node records match volume %q", volumeID)
	}
	return &ISCSIConnection{TargetIQN: matches[0]}, nil
}

func iscsiTargetConnected(targetIQN string) (bool, error) {
	sessions, err := currentISCSISessions()
	if err != nil {
		return false, err
	}
	for _, session := range sessions {
		if session.IQN == targetIQN {
			return true, nil
		}
	}
	return false, nil
}

func nvmeSubNQNForBlockDevice(devicePath string) (string, bool, error) {
	name := filepath.Base(devicePath)
	data, err := os.ReadFile(filepath.Join(sysClassBlockDir, name, "device", "subsysnqn"))
	if err != nil {
		if os.IsNotExist(err) && !strings.HasPrefix(name, "nvme") {
			return "", false, nil
		}
		return "", false, fmt.Errorf("failed to read subsystem NQN for %s: %w", devicePath, err)
	}
	nqn := strings.TrimSpace(string(data))
	if nqn == "" {
		return "", false, fmt.Errorf("empty subsystem NQN for %s", devicePath)
	}
	return nqn, true, nil
}

func isNVMeControllerName(name string) bool {
	if !strings.HasPrefix(name, "nvme") {
		return false
	}
	digits := strings.TrimPrefix(name, "nvme")
	return isAllDigits(digits)
}

func scanNVMeControllers() ([]nvmeController, error) {
	entries, err := os.ReadDir(sysClassNVMeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to list NVMe controllers: %w", err)
	}

	var controllers []nvmeController
	for _, entry := range entries {
		if !isNVMeControllerName(entry.Name()) {
			continue
		}
		base := filepath.Join(sysClassNVMeDir, entry.Name())
		nqnData, err := os.ReadFile(filepath.Join(base, "subsysnqn"))
		if err != nil {
			if os.IsNotExist(err) && pathMissing(base) {
				continue
			}
			return nil, fmt.Errorf("failed to read subsystem NQN for controller %s: %w", entry.Name(), err)
		}
		transportData, err := os.ReadFile(filepath.Join(base, "transport"))
		if err != nil {
			if os.IsNotExist(err) && pathMissing(base) {
				continue
			}
			return nil, fmt.Errorf("failed to read transport for controller %s: %w", entry.Name(), err)
		}
		nqn := strings.TrimSpace(string(nqnData))
		transport := strings.TrimSpace(string(transportData))
		if nqn == "" || transport == "" {
			return nil, fmt.Errorf("controller %s has incomplete NVMe identity", entry.Name())
		}
		controllers = append(controllers, nvmeController{Name: entry.Name(), SubNQN: nqn, Transport: transport})
	}
	sort.Slice(controllers, func(i, j int) bool { return controllers[i].Name < controllers[j].Name })
	return controllers, nil
}

func remoteNVMeControllersForNQN(subnqn string) ([]string, bool, error) {
	controllers, err := scanNVMeControllers()
	if err != nil {
		return nil, false, err
	}
	var remote []string
	hasLocal := false
	for _, controller := range controllers {
		if controller.SubNQN != subnqn {
			continue
		}
		switch controller.Transport {
		case "pcie":
			hasLocal = true
		case "tcp", "rdma", "fc", "loop":
			remote = append(remote, filepath.Join(deviceDir, controller.Name))
		default:
			return nil, false, fmt.Errorf("controller %s has unsupported NVMe transport %q", controller.Name, controller.Transport)
		}
	}
	if hasLocal && len(remote) > 0 {
		return nil, false, fmt.Errorf("NVMe subsystem %q has both local and fabrics controllers", subnqn)
	}
	return remote, hasLocal, nil
}

func nvmeConnectionForDevices(volumeID string, devices []string) (string, []string, bool, error) {
	subnqn := ""
	foundCount := 0
	for _, device := range devices {
		nqn, found, err := nvmeSubNQNForBlockDevice(device)
		if err != nil {
			return "", nil, false, err
		}
		if !found {
			continue
		}
		foundCount++
		if subnqn != "" && subnqn != nqn {
			return "", nil, false, fmt.Errorf("mounted devices map to multiple NVMe subsystems: %q and %q", subnqn, nqn)
		}
		subnqn = nqn
	}
	if foundCount == 0 {
		return "", nil, false, nil
	}
	if foundCount != len(devices) {
		return "", nil, false, fmt.Errorf("mounted device mixes NVMe and non-NVMe paths")
	}
	expectedSuffix := ":" + makeNVMeSubsysName(volumeID)
	if !strings.HasSuffix(subnqn, expectedSuffix) {
		return "", nil, false, fmt.Errorf("NVMe subsystem %q for mounted device does not belong to volume %q", subnqn, volumeID)
	}
	controllers, hasLocal, err := remoteNVMeControllersForNQN(subnqn)
	if err != nil {
		return "", nil, false, err
	}
	if hasLocal {
		return "", nil, false, fmt.Errorf("mounted namespace for %q is local PCIe NVMe, not NVMe-oF", volumeID)
	}
	if len(controllers) == 0 {
		return "", nil, false, fmt.Errorf("no fabrics controller found for mounted NVMe subsystem %q", subnqn)
	}
	return subnqn, controllers, true, nil
}

func findNVMeConnectionByVolumeID(volumeID string) (string, []string, error) {
	controllers, err := scanNVMeControllers()
	if err != nil {
		return "", nil, err
	}
	expectedSuffix := ":" + makeNVMeSubsysName(volumeID)
	matching := make(map[string][]string)
	local := make(map[string]bool)
	for _, controller := range controllers {
		if !strings.HasSuffix(controller.SubNQN, expectedSuffix) {
			continue
		}
		switch controller.Transport {
		case "pcie":
			local[controller.SubNQN] = true
		case "tcp", "rdma", "fc", "loop":
			matching[controller.SubNQN] = append(matching[controller.SubNQN], filepath.Join(deviceDir, controller.Name))
		default:
			return "", nil, fmt.Errorf("controller %s has unsupported NVMe transport %q", controller.Name, controller.Transport)
		}
	}
	for nqn := range matching {
		if local[nqn] {
			return "", nil, fmt.Errorf("NVMe subsystem %q has both local and fabrics controllers", nqn)
		}
	}
	if len(matching) == 0 {
		return "", nil, nil
	}
	if len(matching) > 1 {
		return "", nil, fmt.Errorf("multiple NVMe-oF subsystems match volume %q", volumeID)
	}
	for nqn, paths := range matching {
		sort.Strings(paths)
		return nqn, compactStrings(paths), nil
	}
	return "", nil, nil
}

func nvmeSubsystemConnected(subnqn string) (bool, error) {
	controllers, _, err := remoteNVMeControllersForNQN(subnqn)
	if err != nil {
		return false, err
	}
	return len(controllers) > 0, nil
}

func discoverMountedVolume(volumeID string, mounts []mount.MountPoint, entry *mount.MountPoint) (*volumeState, error) {
	source, fsType, err := resolveMountSource(mounts, entry)
	if err != nil {
		return nil, err
	}
	state := &volumeState{MountPath: entry.Path}
	if strings.HasPrefix(fsType, fsTypeNFS) {
		state.Protocol = ProtocolNFS
		return state, nil
	}
	deviceSource := source
	if !pathWithin(deviceDir, source) {
		deviceSource, err = mountedBlockDevicePath(entry.Path)
		if err != nil {
			return nil, fmt.Errorf("unrecognized mount source %q at %s: %w", source, entry.Path, err)
		}
	}

	devicePath, blockDevices, err := resolveBlockDevices(deviceSource)
	if err != nil {
		return nil, err
	}
	state.DevicePath = devicePath
	state.BlockDevices = blockDevices

	nqn, controllers, foundNVMe, err := nvmeConnectionForDevices(volumeID, blockDevices)
	if err != nil {
		return nil, err
	}
	if foundNVMe {
		state.Protocol = ProtocolNVMeOF
		state.NVMeSubNQN = nqn
		state.NVMeControllers = controllers
		return state, nil
	}

	connection, foundISCSI, err := iscsiConnectionForDevices(volumeID, blockDevices)
	if err != nil {
		return nil, err
	}
	if foundISCSI {
		state.Protocol = ProtocolISCSI
		state.ISCSI = connection
		return state, nil
	}
	return nil, fmt.Errorf("mounted block device %s is neither this volume's iSCSI nor NVMe-oF attachment", source)
}

func discoverDetachedVolume(volumeID string) (*volumeState, error) {
	iscsiConnection, iscsiErr := findISCSIConnectionByVolumeID(volumeID)
	nqn, controllers, nvmeErr := findNVMeConnectionByVolumeID(volumeID)
	if iscsiErr != nil {
		return nil, iscsiErr
	}
	if nvmeErr != nil {
		return nil, nvmeErr
	}
	if iscsiConnection != nil && nqn != "" {
		return nil, fmt.Errorf("both iSCSI and NVMe-oF connections match volume %q", volumeID)
	}
	if iscsiConnection != nil {
		return &volumeState{Protocol: ProtocolISCSI, ISCSI: iscsiConnection}, nil
	}
	if nqn != "" {
		return &volumeState{Protocol: ProtocolNVMeOF, NVMeSubNQN: nqn, NVMeControllers: controllers}, nil
	}
	return &volumeState{}, nil
}

func (s *NodeServer) discoverUnstageState(volumeID, stagingPath string) (*volumeState, error) {
	mounts, err := s.mounter.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list mounts: %w", err)
	}
	filesystemMount := effectiveMount(mounts, stagingPath)
	blockMount := effectiveMount(mounts, rawBlockStagingPath(stagingPath))
	if filesystemMount != nil && blockMount != nil {
		return nil, fmt.Errorf("both filesystem and raw-block staging mounts exist for volume %q", volumeID)
	}
	if filesystemMount != nil {
		return discoverMountedVolume(volumeID, mounts, filesystemMount)
	}
	if blockMount != nil {
		return discoverMountedVolume(volumeID, mounts, blockMount)
	}
	return discoverDetachedVolume(volumeID)
}

func (s *NodeServer) discoverExpandState(volumeID, stagingPath, volumePath string) (*volumeState, error) {
	mounts, err := s.mounter.List()
	if err != nil {
		return nil, fmt.Errorf("failed to list mounts: %w", err)
	}
	var candidates []string
	if stagingPath != "" {
		candidates = append(candidates, rawBlockStagingPath(stagingPath), stagingPath)
	}
	candidates = append(candidates, volumePath)
	seen := make(map[string]bool)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		candidate = filepath.Clean(candidate)
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if entry := effectiveMount(mounts, candidate); entry != nil {
			return discoverMountedVolume(volumeID, mounts, entry)
		}
	}
	return discoverDetachedVolume(volumeID)
}

func pathMissing(path string) bool {
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func pathWithin(root, path string) bool {
	if !filepath.IsAbs(path) {
		return false
	}
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func containsCleanPath(paths []string, target string) bool {
	target = filepath.Clean(target)
	for _, path := range paths {
		if filepath.Clean(path) == target {
			return true
		}
	}
	return false
}

func compactStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}
