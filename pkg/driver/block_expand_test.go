package driver

import (
	"context"

	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/container-storage-interface/spec/lib/go/csi"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/mount-utils"
	"k8s.io/utils/exec"
	testingexec "k8s.io/utils/exec/testing"
)

// A workload-owned partition table is not a filesystem mount-utils can resize.
// Keep this response even in block tests: a regression must fail rather than
// silently succeed against an empty device.
func partitionTableExec() *testingexec.FakeExec {
	cmd := &testingexec.FakeCmd{
		CombinedOutputScript: []testingexec.FakeAction{
			func() ([]byte, []byte, error) { return []byte("DEVNAME=/dev/sda\nPTTYPE=gpt\n"), nil, nil },
		},
	}
	return &testingexec.FakeExec{
		CommandScript: []testingexec.FakeCommandAction{
			func(name string, args ...string) exec.Cmd { return testingexec.InitFakeCmd(cmd, name, args...) },
		},
	}
}

func TestIsBlockVolumeExpansion(t *testing.T) {
	dir := t.TempDir()
	mountPath := filepath.Join(dir, "globalmount")
	if err := os.Mkdir(mountPath, 0o750); err != nil {
		t.Fatal(err)
	}

	// A regular file stands in for a bind-mounted block device; both are
	// non-directories, and creating a real device would require privileges.
	devicePath := filepath.Join(dir, "pvc-abc-device")
	if err := os.WriteFile(devicePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	missingPath := filepath.Join(dir, "gone")
	blockCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
	}
	mountCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: DefaultFSType}},
	}
	accessModeOnly := &csi.VolumeCapability{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}

	tests := []struct {
		name       string
		capability *csi.VolumeCapability
		path       string
		want       bool
	}{
		{"block capability", blockCap, devicePath, true},
		{"block capability on a directory", blockCap, mountPath, true},
		{"block capability on a missing path", blockCap, missingPath, true},
		{"mount capability on a directory", mountCap, mountPath, false},
		{"no capability on a directory", nil, mountPath, false},
		{"no capability on a device", nil, devicePath, true},
		{"access mode only on a device", accessModeOnly, devicePath, true},
		{"access mode only on a directory", accessModeOnly, mountPath, false},
		// A CO may attach the StorageClass fsType even to a block PV.
		{"mount capability on a device", mountCap, devicePath, true},
		{"empty capability on a device", &csi.VolumeCapability{}, devicePath, true},
		{"missing path", nil, missingPath, false},
		{"mount capability on a missing path", mountCap, missingPath, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isBlockVolumeExpansion(tt.capability, tt.path); got != tt.want {
				t.Errorf("isBlockVolumeExpansion() = %v, want %v", got, tt.want)
			}
		})
	}
}

// newBlockExpandTestHandler isolates rescans and filesystem tools from the host.
func newBlockExpandTestHandler(t *testing.T, protocol string) (ProtocolHandler, *testingexec.FakeExec, func()) {
	t.Helper()
	fake := partitionTableExec()
	mounter := &mount.SafeFormatAndMount{Interface: mount.NewFakeMounter(nil), Exec: fake}

	switch protocol {
	case ProtocolISCSI:
		var calls int
		handler := &ISCSIHandler{
			mounter: mounter,
			resizer: mount.NewResizeFs(fake),
			log:     logr.Discard(),
			expandDevice: func(devicePath string, blockDevices []string) error {
				calls++
				if devicePath != "/dev/sda" || len(blockDevices) != 1 || blockDevices[0] != "/dev/sda" {
					t.Errorf("expand device = %q, paths = %v", devicePath, blockDevices)
				}
				if fake.CommandCalls != 0 {
					t.Error("filesystem was probed before device expansion")
				}
				return nil
			},
		}
		return handler, fake, func() {
			t.Helper()
			if calls != 1 {
				t.Errorf("device expansion calls = %d, want 1", calls)
			}
		}
	case ProtocolNVMeOF:
		calls := mockNVMeExec(t)
		handler := &NVMeOFHandler{
			mounter: mounter,
			resizer: mount.NewResizeFs(fake),
			log:     logr.Discard(),
		}
		return handler, fake, func() {
			t.Helper()
			if len(*calls) != 1 {
				t.Errorf("NVMe commands = %+v, want one namespace rescan", *calls)
				return
			}
			call := (*calls)[0]
			if call.name != "nvme" || len(call.args) != 2 || call.args[0] != "ns-rescan" || call.args[1] != "/dev/nvme0" {
				t.Errorf("NVMe command = %+v, want nvme ns-rescan /dev/nvme0", call)
			}
		}
	default:
		t.Fatalf("unsupported test protocol %q", protocol)
		return nil, nil, nil
	}
}

func testBlockExpand(t *testing.T, protocol string, isBlock bool) {
	t.Helper()
	const volumeID = "tank/pvc-abc"
	handler, fake, checkRescan := newBlockExpandTestHandler(t, protocol)
	req := &ExpandRequest{
		VolumeID:      volumeID,
		VolumePath:    filepath.Join(t.TempDir(), "volume"),
		DevicePath:    "/dev/sda",
		BlockDevices:  []string{"/dev/sda"},
		CapacityBytes: 11 * GiB,
		IsBlockVolume: isBlock,
	}
	if protocol == ProtocolNVMeOF {
		req.DevicePath = "/dev/nvme0n1"
		req.BlockDevices = nil
		req.NVMeControllers = []string{"/dev/nvme0"}
	}
	result, err := handler.Expand(context.Background(), req)
	checkRescan()
	if isBlock {
		if err != nil {
			t.Fatalf("Expand() = %v, want nil", err)
		}
		if result == nil || result.CapacityBytes != req.CapacityBytes {
			t.Errorf("Expand() = %+v, want capacity %d", result, req.CapacityBytes)
		}
		if fake.CommandCalls != 0 {
			t.Errorf("filesystem commands = %d, want none", fake.CommandCalls)
		}
		return
	}

	// The same partition table on a filesystem volume must remain an error;
	// success here would leave the filesystem smaller than the expanded device.
	if err == nil || !strings.Contains(err.Error(), "resize") {
		t.Errorf("Expand() = %v, want a resize failure", err)
	}
	if fake.CommandCalls != 1 {
		t.Errorf("filesystem commands = %d, want one partition-table probe", fake.CommandCalls)
	}
}

func TestISCSIExpand_BlockVolumeSkipsFilesystemResize(t *testing.T) {
	testBlockExpand(t, ProtocolISCSI, true)
}

func TestISCSIExpand_FilesystemVolumeStillResizes(t *testing.T) {
	testBlockExpand(t, ProtocolISCSI, false)
}

func TestNVMeOFExpand_BlockVolumeSkipsFilesystemResize(t *testing.T) {
	testBlockExpand(t, ProtocolNVMeOF, true)
}

func TestNVMeOFExpand_FilesystemVolumeStillResizes(t *testing.T) {
	testBlockExpand(t, ProtocolNVMeOF, false)
}

// Exercise the public RPC too, so a missing IsBlockVolume assignment cannot be
// hidden by tests that construct ExpandRequest directly.
func TestNodeExpandVolume_BlockExpansion(t *testing.T) {
	blockCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Block{Block: &csi.VolumeCapability_BlockVolume{}},
	}
	mountCap := &csi.VolumeCapability{
		AccessType: &csi.VolumeCapability_Mount{Mount: &csi.VolumeCapability_MountVolume{FsType: DefaultFSType}},
	}
	accessModeOnly := &csi.VolumeCapability{
		AccessMode: &csi.VolumeCapability_AccessMode{Mode: csi.VolumeCapability_AccessMode_SINGLE_NODE_WRITER},
	}
	tests := []struct {
		name       string
		capability *csi.VolumeCapability
		directory  bool
		wantBlock  bool
	}{
		{"block capability", blockCap, false, true},
		{"no capability on a device", nil, false, true},
		{"access mode only on a device", accessModeOnly, false, true},
		{"mount capability on a device", mountCap, false, true},
		{"no capability on a directory", nil, true, false},
		{"mount capability on a directory", mountCap, true, false},
	}
	for _, protocol := range []string{ProtocolISCSI, ProtocolNVMeOF} {
		t.Run(protocol, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					const volumeID = "tank/pvc-abc"
					fixture := useDiscoveryFixture(t)
					handler, fake, checkRescan := newBlockExpandTestHandler(t, protocol)
					volumePath := t.TempDir()
					if !tt.directory {
						volumePath = filepath.Join(volumePath, "device")
						if err := os.WriteFile(volumePath, nil, 0o600); err != nil {
							t.Fatal(err)
						}
					}

					devicePath := "/dev/sda"
					if protocol == ProtocolISCSI {
						fixture.addISCSIDevice(t, "sda", 7)
						iscsiGetSessions = func() (string, error) {
							return iscsiSessionLine(7, "10.0.0.10:3260", "iqn.2000-01.io.truenas:csi-tank-pvc-abc"), nil
						}
					} else {
						devicePath = "/dev/nvme0n1"
						fixture.addNVMeDevice(t, "nvme0n1", "nvme0", "nqn.2011-06.com.truenas:csi-tank-pvc-abc", "tcp")
					}
					nodeMounter := mount.NewFakeMounter([]mount.MountPoint{{Device: devicePath, Path: volumePath, Type: "ext4"}})
					server := &NodeServer{
						driver:     &Driver{log: logr.Discard()},
						mounter:    nodeMounter,
						nfsHandler: NewNFSHandler(nodeMounter, logr.Discard()),
					}
					switch h := handler.(type) {
					case *ISCSIHandler:
						server.iscsiHandler = h
					case *NVMeOFHandler:
						server.nvmeofHandler = h
					}
					req := &csi.NodeExpandVolumeRequest{
						VolumeId:         volumeID,
						VolumePath:       volumePath,
						CapacityRange:    &csi.CapacityRange{RequiredBytes: 11 * GiB},
						VolumeCapability: tt.capability,
					}
					result, err := server.NodeExpandVolume(context.Background(), req)
					checkRescan()
					if tt.wantBlock {
						if err != nil {
							t.Fatalf("NodeExpandVolume() = %v, want nil", err)
						}
						if result == nil || result.CapacityBytes != req.CapacityRange.RequiredBytes {
							t.Errorf("NodeExpandVolume() = %+v, want capacity %d", result, req.CapacityRange.RequiredBytes)
						}
						if fake.CommandCalls != 0 {
							t.Errorf("filesystem commands = %d, want none", fake.CommandCalls)
						}
					} else {
						if status.Code(err) != codes.Internal || !strings.Contains(err.Error(), "resize") {
							t.Errorf("NodeExpandVolume() = %v, want Internal resize failure", err)
						}
						if fake.CommandCalls != 1 {
							t.Errorf("filesystem commands = %d, want one partition-table probe", fake.CommandCalls)
						}
					}
				})
			}
		})
	}
}
