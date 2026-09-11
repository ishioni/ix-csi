package driver

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"
	utilexec "k8s.io/utils/exec"
)

type discoveryFixture struct {
	blockDir        string
	nvmeDir         string
	iscsiSessionDir string
}

func useDiscoveryFixture(t *testing.T) *discoveryFixture {
	t.Helper()
	root := t.TempDir()
	fixture := &discoveryFixture{
		blockDir:        filepath.Join(root, "block"),
		nvmeDir:         filepath.Join(root, "nvme"),
		iscsiSessionDir: filepath.Join(root, "iscsi-session"),
	}
	if err := os.MkdirAll(fixture.blockDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixture.nvmeDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fixture.iscsiSessionDir, 0o750); err != nil {
		t.Fatal(err)
	}

	oldBlockDir := sysClassBlockDir
	oldNVMeDir := sysClassNVMeDir
	oldISCSISessionDir := sysClassISCSISessionDir
	oldSysDevDir := sysDevBlockDir
	oldISCSINodeDBDir := iscsiNodeDBDir
	oldDeviceDir := deviceDir
	oldEval := evalDeviceSymlink
	oldSessions := iscsiGetSessions
	oldLogoutTarget := logoutISCSITarget
	oldLogout := iscsiLogout
	oldDelete := iscsiDeleteDBEntry
	sysClassBlockDir = fixture.blockDir
	sysClassNVMeDir = fixture.nvmeDir
	sysClassISCSISessionDir = fixture.iscsiSessionDir
	sysDevBlockDir = filepath.Join(root, "dev-block")
	iscsiNodeDBDir = filepath.Join(root, "iscsi-nodes")
	if err := os.MkdirAll(iscsiNodeDBDir, 0o750); err != nil {
		t.Fatal(err)
	}
	deviceDir = "/dev"
	evalDeviceSymlink = func(path string) (string, error) { return path, nil }
	iscsiGetSessions = func() (string, error) { return "", nil }
	t.Cleanup(func() {
		sysClassBlockDir = oldBlockDir
		sysClassNVMeDir = oldNVMeDir
		sysClassISCSISessionDir = oldISCSISessionDir
		sysDevBlockDir = oldSysDevDir
		iscsiNodeDBDir = oldISCSINodeDBDir
		deviceDir = oldDeviceDir
		evalDeviceSymlink = oldEval
		iscsiGetSessions = oldSessions
		logoutISCSITarget = oldLogoutTarget
		iscsiLogout = oldLogout
		iscsiDeleteDBEntry = oldDelete
	})
	return fixture
}

func (f *discoveryFixture) addISCSIDevice(t *testing.T, name string, sessionID int) {
	t.Helper()
	base := filepath.Join(f.blockDir, name)
	if err := os.MkdirAll(filepath.Join(base, "slaves"), 0o750); err != nil {
		t.Fatal(err)
	}
	root := filepath.Dir(f.blockDir)
	actual := filepath.Join(root, "devices", "platform", "host6", "session"+itoa(sessionID), "target6:0:0", "6:0:0:0")
	if err := os.MkdirAll(actual, 0o750); err != nil {
		t.Fatal(err)
	}
	aliases := filepath.Join(root, "scsi")
	if err := os.MkdirAll(aliases, 0o750); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(aliases, name)
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	// The raw class symlink does not contain sessionN; only its resolved target does.
	if err := os.Symlink(filepath.Join("..", "..", "scsi", name), filepath.Join(base, "device")); err != nil {
		t.Fatal(err)
	}
}

func (f *discoveryFixture) addISCSISession(t *testing.T, sessionID int, targetIQN string) {
	t.Helper()
	base := filepath.Join(f.iscsiSessionDir, "session"+itoa(sessionID))
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "targetname"), []byte(targetIQN+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *discoveryFixture) addMultipathDevice(t *testing.T, name string, slaves ...string) {
	t.Helper()
	base := filepath.Join(f.blockDir, name, "slaves")
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, slave := range slaves {
		if err := os.Symlink(filepath.Join("..", "..", slave), filepath.Join(base, slave)); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *discoveryFixture) addNVMeDevice(t *testing.T, device, controller, nqn, transport string) {
	t.Helper()
	blockBase := filepath.Join(f.blockDir, device)
	if err := os.MkdirAll(filepath.Join(blockBase, "slaves"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(blockBase, "device"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blockBase, "device", "subsysnqn"), []byte(nqn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	controllerBase := filepath.Join(f.nvmeDir, controller)
	if err := os.MkdirAll(controllerBase, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controllerBase, "subsysnqn"), []byte(nqn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controllerBase, "transport"), []byte(transport+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	var digits [20]byte
	pos := len(digits)
	for value > 0 {
		pos--
		digits[pos] = byte('0' + value%10)
		value /= 10
	}
	return string(digits[pos:])
}

type recordingMounter struct {
	mount.Interface
	sources []string
}

func (m *recordingMounter) Mount(source, target, fsType string, options []string) error {
	m.sources = append(m.sources, source)
	return m.Interface.Mount(source, target, fsType, options)
}

type hostMirrorPropagatingMounter struct {
	mount.Interface
}

func (m *hostMirrorPropagatingMounter) Unmount(target string) error {
	if err := m.Interface.Unmount(target); err != nil {
		return err
	}
	return m.Interface.Unmount("/host" + target)
}

func newTestNodeServer(mounts []mount.MountPoint) (*NodeServer, *mount.FakeMounter) {
	log := logr.Discard()
	fake := mount.NewFakeMounter(mounts)
	safe := &mount.SafeFormatAndMount{Interface: fake, Exec: utilexec.New()}
	driver := &Driver{
		log: log,
		volumeCaps: []*csi.VolumeCapability_AccessMode{
			{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
			{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_READER_ONLY},
		},
	}
	return &NodeServer{
		driver:        driver,
		mounter:       fake,
		iscsiHandler:  &ISCSIHandler{mounter: safe, resizer: mount.NewResizeFs(safe.Exec), log: log},
		nvmeofHandler: &NVMeOFHandler{mounter: safe, resizer: mount.NewResizeFs(safe.Exec), log: log},
		nfsHandler:    NewNFSHandler(fake, log),
	}, fake
}

func stagingDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "globalmount")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func iscsiSessionLine(id int, portal, iqn string) string {
	return "tcp: [" + itoa(id) + "] " + portal + ",1 " + iqn + " (non-flash)\n"
}

func mountedISCSIStageRequest(volumeID, stagingPath string) *csi.NodeStageVolumeRequest {
	return &csi.NodeStageVolumeRequest{
		VolumeId:          volumeID,
		StagingTargetPath: stagingPath,
		PublishContext:    map[string]string{PublishContextProtocol: ProtocolISCSI},
		VolumeCapability: &csi.VolumeCapability{
			AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: DefaultFSType}},
			AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
		},
	}
}

func TestParseISCSISessionsPreservesPortalPort(t *testing.T) {
	output := iscsiSessionLine(7, "10.0.0.10:3261", "iqn.2000-01.io.truenas:csi-tank-pvc-abc")
	sessions, err := parseISCSISessions(output)
	if err != nil {
		t.Fatalf("parseISCSISessions() = %v", err)
	}
	want := []iscsiSession{{ID: 7, Portal: "10.0.0.10:3261", IQN: "iqn.2000-01.io.truenas:csi-tank-pvc-abc"}}
	if !reflect.DeepEqual(sessions, want) {
		t.Fatalf("sessions = %+v, want %+v", sessions, want)
	}
	if _, err := parseISCSISessions("not a session"); err == nil {
		t.Fatal("malformed session output must not be treated as an empty session list")
	}
}

func TestExternalMountReferencesIgnoresHostMirror(t *testing.T) {
	staging := "/var/lib/kubelet/plugins/kubernetes.io/csi/csi.ix-csi.io/hash/globalmount"
	published := "/var/lib/kubelet/pods/pod-id/volumes/kubernetes.io~csi/pvc/mount"
	refs := externalMountReferences(staging, []string{
		"/host" + staging,
		published,
		"/host" + published,
	})
	if want := []string{published}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("externalMountReferences() = %v, want %v", refs, want)
	}
}

func TestNodeUnstageVolumeWithoutISCSIEvidenceDoesNotListSessions(t *testing.T) {
	useDiscoveryFixture(t)
	sessionCalls := 0
	iscsiGetSessions = func() (string, error) {
		sessionCalls++
		return "", exec.ErrNotFound
	}

	staging := stagingDir(t)
	node, _ := newTestNodeServer(nil)
	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: "tank/pvc-nfs", StagingTargetPath: staging})
	if err != nil {
		t.Fatalf("NodeUnstageVolume() = %v", err)
	}
	if sessionCalls != 0 {
		t.Fatalf("iscsi session calls = %d, want 0", sessionCalls)
	}
}

func TestNodeUnstageVolumeWithISCSIEvidenceFailsClosedWhenSessionsCannotBeListed(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const (
		volumeID = "tank/pvc-iscsi"
		iqn      = "iqn.2000-01.io.truenas:csi-tank-pvc-iscsi"
	)
	fixture.addISCSISession(t, 7, iqn)
	sessionCalls := 0
	iscsiGetSessions = func() (string, error) {
		sessionCalls++
		return "", exec.ErrNotFound
	}

	staging := stagingDir(t)
	node, _ := newTestNodeServer(nil)
	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnstageVolume() = %v, want Internal", err)
	}
	if sessionCalls != 1 {
		t.Fatalf("iscsi session calls = %d, want 1", sessionCalls)
	}
}

func TestNodeUnstageVolumeWithUnconfirmedISCSIEvidenceFailsClosed(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const (
		volumeID = "tank/pvc-iscsi"
		iqn      = "iqn.2000-01.io.truenas:csi-tank-pvc-iscsi"
	)
	fixture.addISCSISession(t, 7, iqn)
	iscsiGetSessions = func() (string, error) { return "", nil }

	staging := stagingDir(t)
	node, _ := newTestNodeServer(nil)
	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnstageVolume() = %v, want Internal", err)
	}
}

func TestNodeUnstageVolumeRejectsConflictingISCSIEvidence(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const (
		volumeID = "tank/pvc-iscsi"
		sysfsIQN = "iqn.old.example:csi-tank-pvc-iscsi"
		admIQN   = "iqn.new.example:csi-tank-pvc-iscsi"
	)
	fixture.addISCSISession(t, 7, sysfsIQN)
	iscsiGetSessions = func() (string, error) {
		return iscsiSessionLine(7, "10.0.0.10:3260", admIQN), nil
	}

	staging := stagingDir(t)
	node, _ := newTestNodeServer(nil)
	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnstageVolume() = %v, want Internal", err)
	}
}

func TestISCSITargetConnectedChecksSysfs(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const iqn = "iqn.2000-01.io.truenas:csi-tank-pvc-iscsi"
	fixture.addISCSISession(t, 7, iqn)
	iscsiGetSessions = func() (string, error) { return "", nil }

	connected, err := iscsiTargetConnected(iqn)
	if err != nil {
		t.Fatal(err)
	}
	if !connected {
		t.Fatal("sysfs iSCSI session was not treated as connected")
	}
}

func TestISCSIUnstageRejectsDifferentRemainingTargetForVolume(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const (
		volumeID = "tank/pvc-iscsi"
		oldIQN   = "iqn.old.example:csi-tank-pvc-iscsi"
		newIQN   = "iqn.new.example:csi-tank-pvc-iscsi"
	)
	fixture.addISCSISession(t, 7, oldIQN)
	iscsiGetSessions = func() (string, error) { return "", nil }
	logoutISCSITarget = func(string, []string) error { return nil }

	node, _ := newTestNodeServer(nil)
	err := node.iscsiHandler.Unstage(context.Background(), &UnstageRequest{
		VolumeID: volumeID,
		ISCSI:    &ISCSIConnection{TargetIQN: newIQN},
	})
	if err == nil {
		t.Fatal("Unstage() succeeded while another matching iSCSI target remained")
	}
}

func TestLogoutISCSITargetPreservesCustomPort(t *testing.T) {
	useDiscoveryFixture(t)
	var gotPortal, gotIQN string
	iscsiLogout = func(iqn, portal string) error {
		gotIQN, gotPortal = iqn, portal
		return nil
	}
	iscsiDeleteDBEntry = func(string) error { return nil }

	const iqn = "iqn.2000-01.io.truenas:csi-tank-pvc-abc"
	if err := logoutISCSITarget(iqn, []string{"10.0.0.10:3261"}); err != nil {
		t.Fatal(err)
	}
	if gotIQN != iqn || gotPortal != "10.0.0.10:3261" {
		t.Fatalf("logout got %q at %q", gotIQN, gotPortal)
	}
}

func TestNodeStageVolumeValidatesExistingMountWithoutRestaging(t *testing.T) {
	for _, tt := range []struct {
		name string
		iqn  string
		ok   bool
	}{
		{name: "matching target", iqn: "iqn.2000-01.io.truenas:csi-tank-pvc-abc", ok: true},
		{name: "different target", iqn: "iqn.2000-01.io.truenas:csi-tank-pvc-other", ok: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := useDiscoveryFixture(t)
			fixture.addISCSIDevice(t, "sda", 7)
			staging := stagingDir(t)
			iscsiGetSessions = func() (string, error) {
				return iscsiSessionLine(7, "10.0.0.10:3260", tt.iqn), nil
			}
			node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sda", Path: staging, Type: "ext4"}})
			_, err := node.NodeStageVolume(context.Background(), mountedISCSIStageRequest("tank/pvc-abc", staging))
			if (err == nil) != tt.ok {
				t.Fatalf("NodeStageVolume() = %v, want success %v", err, tt.ok)
			}
			if effectiveMount(fake.MountPoints, staging) == nil {
				t.Fatal("existing mount was destructively removed")
			}
		})
	}
}

func TestNodeStageVolumeRejectsOppositeAccessType(t *testing.T) {
	useDiscoveryFixture(t)
	staging := stagingDir(t)
	anchor := rawBlockStagingPath(staging)
	if err := os.WriteFile(anchor, nil, 0o660); err != nil {
		t.Fatal(err)
	}
	node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sda", Path: anchor}})
	_, err := node.NodeStageVolume(context.Background(), mountedISCSIStageRequest("tank/pvc-abc", staging))
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("NodeStageVolume() = %v, want FailedPrecondition", err)
	}
	if effectiveMount(fake.MountPoints, anchor) == nil {
		t.Fatal("opposite-mode staging mount was removed")
	}
}

func TestNodePublishVolumeRejectsUnrelatedExistingMount(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sda", 7)
	const iqn = "iqn.2000-01.io.truenas:csi-tank-pvc-abc"
	iscsiGetSessions = func() (string, error) {
		return iscsiSessionLine(7, "10.0.0.10:3260", iqn), nil
	}
	staging := stagingDir(t)
	target := stagingDir(t)
	node, _ := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sda", Path: target, Type: "ext4"}})
	_, err := node.NodePublishVolume(context.Background(), &csi.NodePublishVolumeRequest{
		VolumeId:          "tank/pvc-abc",
		StagingTargetPath: staging,
		TargetPath:        target,
		PublishContext:    map[string]string{PublishContextProtocol: ProtocolISCSI},
		VolumeCapability:  mountedISCSIStageRequest("", "").VolumeCapability,
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("NodePublishVolume() = %v, want FailedPrecondition", err)
	}
}

func TestNodeUnstageVolumeRecoversISCSIStateAfterUpgrade(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sda", 7)
	const (
		volumeID = "tank/pvc-abc"
		iqn      = "iqn.2000-01.io.truenas:csi-tank-pvc-abc"
	)
	staging := stagingDir(t)
	sessionCalls := 0
	iscsiGetSessions = func() (string, error) {
		sessionCalls++
		if sessionCalls == 1 {
			return iscsiSessionLine(7, "10.0.0.10:3261", iqn), nil
		}
		return "", nil
	}
	var loggedOut *ISCSIConnection
	logoutISCSITarget = func(target string, portals []string) error {
		loggedOut = &ISCSIConnection{TargetIQN: target, Portals: append([]string(nil), portals...)}
		return nil
	}

	node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sda", Path: staging, Type: "ext4"}})
	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if err != nil {
		t.Fatalf("NodeUnstageVolume() = %v", err)
	}
	if loggedOut == nil || loggedOut.TargetIQN != iqn || !reflect.DeepEqual(loggedOut.Portals, []string{"10.0.0.10:3261"}) {
		t.Fatalf("logout identity = %+v", loggedOut)
	}
	if effectiveMount(fake.MountPoints, staging) != nil {
		t.Fatal("staging mount remained after successful unstage")
	}
}

func TestNodeUnstageVolumeRetriesISCSILogoutAfterUnmount(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sda", 7)
	const (
		volumeID = "tank/pvc-abc"
		iqn      = "iqn.2000-01.io.truenas:csi-tank-pvc-abc"
	)
	fixture.addISCSISession(t, 7, iqn)
	staging := stagingDir(t)
	sessionCalls := 0
	iscsiGetSessions = func() (string, error) {
		sessionCalls++
		if sessionCalls <= 2 {
			return iscsiSessionLine(7, "10.0.0.10:3260", iqn), nil
		}
		return "", nil
	}
	logoutCalls := 0
	logoutISCSITarget = func(string, []string) error {
		logoutCalls++
		if logoutCalls == 1 {
			return syscall.EIO
		}
		return os.RemoveAll(filepath.Join(fixture.iscsiSessionDir, "session7"))
	}

	node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sda", Path: staging, Type: "ext4"}})
	req := &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging}
	if _, err := node.NodeUnstageVolume(context.Background(), req); status.Code(err) != codes.Internal {
		t.Fatalf("first NodeUnstageVolume() = %v, want Internal", err)
	}
	if effectiveMount(fake.MountPoints, staging) != nil {
		t.Fatal("staging mount should be gone before the failed logout")
	}
	if _, err := node.NodeUnstageVolume(context.Background(), req); err != nil {
		t.Fatalf("retry NodeUnstageVolume() = %v", err)
	}
	if logoutCalls != 2 {
		t.Fatalf("logout calls = %d, want 2", logoutCalls)
	}
}

func TestNodeUnstageVolumeRetriesISCSINodeRecordDeletion(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sda", 7)
	const (
		volumeID = "tank/pvc-abc"
		iqn      = "iqn.2000-01.io.truenas:csi-tank-pvc-abc"
	)
	if err := os.MkdirAll(filepath.Join(iscsiNodeDBDir, iqn), 0o750); err != nil {
		t.Fatal(err)
	}
	staging := stagingDir(t)
	sessionCalls := 0
	iscsiGetSessions = func() (string, error) {
		sessionCalls++
		if sessionCalls == 1 {
			return iscsiSessionLine(7, "10.0.0.10:3260", iqn), nil
		}
		return "", nil
	}
	iscsiLogout = func(string, string) error { return nil }
	deleteCalls := 0
	iscsiDeleteDBEntry = func(target string) error {
		deleteCalls++
		if deleteCalls == 1 {
			return syscall.EIO
		}
		return os.RemoveAll(filepath.Join(iscsiNodeDBDir, target))
	}
	logoutISCSITarget = logoutISCSITargetImpl

	node, _ := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sda", Path: staging, Type: "ext4"}})
	req := &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging}
	if _, err := node.NodeUnstageVolume(context.Background(), req); status.Code(err) != codes.Internal {
		t.Fatalf("first NodeUnstageVolume() = %v, want Internal", err)
	}
	if _, err := node.NodeUnstageVolume(context.Background(), req); err != nil {
		t.Fatalf("retry NodeUnstageVolume() = %v", err)
	}
	if deleteCalls != 2 {
		t.Fatalf("node-record deletion calls = %d, want 2", deleteCalls)
	}
}

func TestNodeUnstageVolumeRetriesNVMeDisconnectAfterUnmount(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const (
		volumeID = "tank/pvc-abc"
		nqn      = "nqn.2011-06.com.truenas:csi-tank-pvc-abc"
	)
	fixture.addNVMeDevice(t, "nvme2n1", "nvme2", nqn, "tcp")
	iscsiSessionCalls := 0
	iscsiGetSessions = func() (string, error) {
		iscsiSessionCalls++
		return "", exec.ErrNotFound
	}
	staging := stagingDir(t)
	anchor := rawBlockStagingPath(staging)
	if err := os.WriteFile(anchor, nil, 0o660); err != nil {
		t.Fatal(err)
	}
	node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/nvme2n1", Path: anchor, Type: ""}})

	disconnectCalls := 0
	oldExec := nvmeExecCommand
	nvmeExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "disconnect" {
			disconnectCalls++
			if disconnectCalls == 1 {
				return exec.CommandContext(ctx, "false")
			}
			_ = os.RemoveAll(filepath.Join(fixture.nvmeDir, "nvme2"))
		}
		return exec.CommandContext(ctx, "true")
	}
	t.Cleanup(func() { nvmeExecCommand = oldExec })

	req := &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging}
	if _, err := node.NodeUnstageVolume(context.Background(), req); status.Code(err) != codes.Internal {
		t.Fatalf("first NodeUnstageVolume() = %v, want Internal", err)
	}
	if effectiveMount(fake.MountPoints, anchor) != nil {
		t.Fatal("raw staging anchor should be unmounted before disconnect")
	}
	if _, err := node.NodeUnstageVolume(context.Background(), req); err != nil {
		t.Fatalf("retry NodeUnstageVolume() = %v", err)
	}
	if disconnectCalls != 2 {
		t.Fatalf("disconnect calls = %d, want 2", disconnectCalls)
	}
	if iscsiSessionCalls != 0 {
		t.Fatalf("iscsi session calls = %d, want 0", iscsiSessionCalls)
	}
	if _, err := os.Stat(anchor); !os.IsNotExist(err) {
		t.Fatalf("raw staging anchor file remained after retry: %v", err)
	}
}

func TestNodeUnstageVolumeIgnoresHostMirrorOfStagingMount(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const (
		volumeID = "tank/pvc-abc"
		nqn      = "nqn.2011-06.com.truenas:csi-tank-pvc-abc"
	)
	fixture.addNVMeDevice(t, "nvme2n1", "nvme2", nqn, "tcp")
	staging := stagingDir(t)
	hostMirror := "/host" + staging
	node, fake := newTestNodeServer([]mount.MountPoint{
		{Device: "/dev/nvme2n1", Path: staging, Type: "ext4"},
		{Device: "/dev/nvme2n1", Path: hostMirror, Type: "ext4"},
	})
	node.mounter = &hostMirrorPropagatingMounter{Interface: fake}

	disconnectCalls := 0
	oldExec := nvmeExecCommand
	nvmeExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "disconnect" {
			disconnectCalls++
			_ = os.RemoveAll(filepath.Join(fixture.nvmeDir, "nvme2"))
		}
		return exec.CommandContext(ctx, "true")
	}
	t.Cleanup(func() { nvmeExecCommand = oldExec })

	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if err != nil {
		t.Fatalf("NodeUnstageVolume() = %v", err)
	}
	if disconnectCalls != 1 {
		t.Fatalf("disconnect calls = %d, want 1", disconnectCalls)
	}
	if effectiveCanonicalMount(fake.MountPoints, staging) != nil {
		t.Fatal("staging mount or its host mirror remained after successful unstage")
	}
}

func TestNodeUnstageVolumeFailsClosedWhenHostMirrorRemains(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	const (
		volumeID = "tank/pvc-abc"
		nqn      = "nqn.2011-06.com.truenas:csi-tank-pvc-abc"
	)
	fixture.addNVMeDevice(t, "nvme2n1", "nvme2", nqn, "tcp")
	staging := stagingDir(t)
	hostMirror := "/host" + staging
	node, fake := newTestNodeServer([]mount.MountPoint{
		{Device: "/dev/nvme2n1", Path: staging, Type: "ext4"},
		{Device: "/dev/nvme2n1", Path: hostMirror, Type: "ext4"},
	})

	disconnectCalls := 0
	oldExec := nvmeExecCommand
	nvmeExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
		if len(args) > 0 && args[0] == "disconnect" {
			disconnectCalls++
		}
		return exec.CommandContext(ctx, "true")
	}
	t.Cleanup(func() { nvmeExecCommand = oldExec })

	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnstageVolume() = %v, want Internal", err)
	}
	if disconnectCalls != 0 {
		t.Fatalf("disconnect calls = %d, want 0", disconnectCalls)
	}
	if effectiveMount(fake.MountPoints, hostMirror) == nil {
		t.Fatal("test host mirror unexpectedly disappeared")
	}
	_, retryErr := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if status.Code(retryErr) != codes.Internal {
		t.Fatalf("retry NodeUnstageVolume() = %v, want Internal", retryErr)
	}
	if disconnectCalls != 0 {
		t.Fatalf("disconnect calls after retry = %d, want 0", disconnectCalls)
	}
}

func TestNodeUnstageVolumeFailsClosedForRawBlockHostMirror(t *testing.T) {
	useDiscoveryFixture(t)
	staging := stagingDir(t)
	hostMirror := "/host" + rawBlockStagingPath(staging)
	node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/nvme2n1", Path: hostMirror}})

	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: "tank/pvc-block", StagingTargetPath: staging})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnstageVolume() = %v, want Internal", err)
	}
	if effectiveMount(fake.MountPoints, hostMirror) == nil {
		t.Fatal("raw-block host mirror was removed despite uncertain mount propagation")
	}
}

func TestNodeUnstageVolumeRejectsActivePublishReference(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sda", 7)
	const (
		volumeID = "tank/pvc-abc"
		iqn      = "iqn.2000-01.io.truenas:csi-tank-pvc-abc"
	)
	staging := stagingDir(t)
	published := filepath.Join(t.TempDir(), "published")
	iscsiGetSessions = func() (string, error) {
		return iscsiSessionLine(7, "10.0.0.10:3260", iqn), nil
	}
	logoutCalls := 0
	logoutISCSITarget = func(string, []string) error { logoutCalls++; return nil }

	node, fake := newTestNodeServer([]mount.MountPoint{
		{Device: "/dev/sda", Path: staging, Type: "ext4"},
		{Device: "/dev/sda", Path: published, Type: "ext4"},
	})
	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("NodeUnstageVolume() = %v, want FailedPrecondition", err)
	}
	if logoutCalls != 0 || effectiveMount(fake.MountPoints, staging) == nil {
		t.Fatal("active volume was unmounted or disconnected")
	}
}

func TestNodeUnstageVolumeInspectionFailureLeavesMount(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sda", 7)
	staging := stagingDir(t)
	iscsiGetSessions = func() (string, error) { return "", syscall.EIO }
	node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sda", Path: staging, Type: "ext4"}})

	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: "tank/pvc-abc", StagingTargetPath: staging})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnstageVolume() = %v, want Internal", err)
	}
	if effectiveMount(fake.MountPoints, staging) == nil {
		t.Fatal("mount was removed despite uncertain session inspection")
	}
}

func TestNodeUnstageVolumeRejectsUnknownBlockMount(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sdz", 0)
	// Replace the session symlink with a local SCSI path that has no session component.
	if err := os.Remove(filepath.Join(fixture.blockDir, "sdz", "device")); err != nil {
		t.Fatal(err)
	}
	localDevice := filepath.Join(filepath.Dir(fixture.blockDir), "devices", "platform", "host0", "target0:0:0", "0:0:0:0")
	if err := os.MkdirAll(localDevice, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(localDevice, filepath.Join(fixture.blockDir, "sdz", "device")); err != nil {
		t.Fatal(err)
	}
	staging := stagingDir(t)
	node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/sdz", Path: staging, Type: "ext4"}})

	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: "tank/pvc-abc", StagingTargetPath: staging})
	if status.Code(err) != codes.Internal {
		t.Fatalf("NodeUnstageVolume() = %v, want Internal", err)
	}
	if effectiveMount(fake.MountPoints, staging) == nil {
		t.Fatal("unknown block mount was silently removed")
	}
}

func TestNodeUnstageVolumeNoMountOrConnectionIsIdempotent(t *testing.T) {
	useDiscoveryFixture(t)
	staging := stagingDir(t)
	node, _ := newTestNodeServer(nil)
	if _, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: "tank/pvc-missing", StagingTargetPath: staging}); err != nil {
		t.Fatalf("NodeUnstageVolume() = %v", err)
	}
}

func TestNodeUnstageVolumeDiscoversNVMeOFAndRejectsLocalNVMe(t *testing.T) {
	for _, tt := range []struct {
		name      string
		transport string
		wantCode  codes.Code
	}{
		{name: "fabrics", transport: "tcp", wantCode: codes.OK},
		{name: "local PCIe", transport: "pcie", wantCode: codes.Internal},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fixture := useDiscoveryFixture(t)
			const (
				volumeID = "tank/pvc-abc"
				nqn      = "nqn.2011-06.com.truenas:csi-tank-pvc-abc"
			)
			fixture.addNVMeDevice(t, "nvme2n1", "nvme2", nqn, tt.transport)
			staging := stagingDir(t)
			node, fake := newTestNodeServer([]mount.MountPoint{{Device: "/dev/nvme2n1", Path: staging, Type: "ext4"}})

			oldExec := nvmeExecCommand
			nvmeExecCommand = func(ctx context.Context, name string, args ...string) *exec.Cmd {
				if tt.transport == "tcp" && len(args) > 0 && args[0] == "disconnect" {
					_ = os.RemoveAll(filepath.Join(fixture.nvmeDir, "nvme2"))
				}
				return exec.CommandContext(ctx, "true")
			}
			t.Cleanup(func() { nvmeExecCommand = oldExec })

			_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: staging})
			if status.Code(err) != tt.wantCode {
				t.Fatalf("NodeUnstageVolume() = %v, want %v", err, tt.wantCode)
			}
			if tt.wantCode == codes.OK && effectiveMount(fake.MountPoints, staging) != nil {
				t.Fatal("NVMe-oF mount remained after cleanup")
			}
			if tt.wantCode != codes.OK && effectiveMount(fake.MountPoints, staging) == nil {
				t.Fatal("local NVMe mount was removed")
			}
		})
	}
}

func TestISCSIMultipathDiscovery(t *testing.T) {
	fixture := useDiscoveryFixture(t)
	fixture.addISCSIDevice(t, "sda", 7)
	fixture.addISCSIDevice(t, "sdb", 8)
	fixture.addMultipathDevice(t, "dm-3", "sda", "sdb")
	const iqn = "iqn.2000-01.io.truenas:csi-tank-pvc-abc"
	iscsiGetSessions = func() (string, error) {
		return iscsiSessionLine(7, "10.0.0.10:3260", iqn) + iscsiSessionLine(8, "10.0.0.11:3260", iqn), nil
	}

	device, leaves, err := resolveBlockDevices("/dev/dm-3")
	if err != nil {
		t.Fatal(err)
	}
	connection, found, err := iscsiConnectionForDevices("tank/pvc-abc", leaves)
	if err != nil {
		t.Fatal(err)
	}
	if device != "/dev/dm-3" || !found || !reflect.DeepEqual(connection.Portals, []string{"10.0.0.10:3260", "10.0.0.11:3260"}) {
		t.Fatalf("device=%q leaves=%v connection=%+v found=%v", device, leaves, connection, found)
	}
}

func TestRawBlockStagingAnchorAndPublishSource(t *testing.T) {
	staging := t.TempDir()
	target := filepath.Join(t.TempDir(), "volume")
	fake := mount.NewFakeMounter(nil)
	recorder := &recordingMounter{Interface: fake}
	if err := stageBlockDevice(recorder, "/dev/sda", staging); err != nil {
		t.Fatal(err)
	}
	anchor := rawBlockStagingPath(staging)
	anchorMount := effectiveMount(fake.MountPoints, anchor)
	if anchorMount == nil || anchorMount.Device != "/dev/sda" {
		t.Fatalf("anchor mount = %+v", anchorMount)
	}
	if err := publishBlockDevice(recorder, staging, target, false); err != nil {
		t.Fatal(err)
	}
	targetMount := effectiveMount(fake.MountPoints, target)
	if targetMount == nil || targetMount.Device != "/dev/sda" {
		t.Fatalf("published mount = %+v", targetMount)
	}
	if !reflect.DeepEqual(recorder.sources, []string{"/dev/sda", anchor}) {
		t.Fatalf("mount sources = %v, want device then staging anchor", recorder.sources)
	}
	refs, err := fake.GetMountRefs(anchor)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(refs, []string{target}) {
		t.Fatalf("staging refs = %v, want %s", refs, target)
	}
}

func TestNodeExpandVolumeDoesNotTreatUnknownStateAsNFS(t *testing.T) {
	useDiscoveryFixture(t)
	volumePath := t.TempDir()
	node, _ := newTestNodeServer(nil)
	_, err := node.NodeExpandVolume(context.Background(), &csi.NodeExpandVolumeRequest{
		VolumeId:      "tank/pvc-abc",
		VolumePath:    volumePath,
		CapacityRange: &csi.CapacityRange{RequiredBytes: 1024},
	})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("NodeExpandVolume() = %v, want NotFound", err)
	}
}

func TestNodeLifecycleUsesOneVolumeLock(t *testing.T) {
	useDiscoveryFixture(t)
	node, _ := newTestNodeServer(nil)
	const volumeID = "tank/pvc-abc"
	if !node.TryAcquireLock(volumeID) {
		t.Fatal("failed to acquire test lock")
	}
	defer node.ReleaseLock(volumeID)

	_, err := node.NodeUnstageVolume(context.Background(), &csi.NodeUnstageVolumeRequest{VolumeId: volumeID, StagingTargetPath: "/staging"})
	if status.Code(err) != codes.Aborted {
		t.Fatalf("NodeUnstageVolume() = %v, want Aborted", err)
	}
}

func TestIsNoISCSIObjectsFound(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 21").Run()
	if !isNoISCSIObjectsFound(err) {
		t.Fatal("iscsiadm exit 21 should mean no objects")
	}
	if isNoISCSIObjectsFound(errors.New("I/O error")) {
		t.Fatal("ordinary errors must not mean no objects")
	}
}
