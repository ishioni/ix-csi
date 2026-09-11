//go:build linux

package driver

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestMountedBlockDevicePathResolvesDeviceNumber(t *testing.T) {
	root := t.TempDir()
	devBlock := filepath.Join(root, "dev-block")
	deviceTarget := filepath.Join(root, "devices", "block", "sda")
	if err := os.MkdirAll(devBlock, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(deviceTarget, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(deviceTarget, filepath.Join(devBlock, "8:1")); err != nil {
		t.Fatal(err)
	}

	deviceNode := filepath.Join(root, "mounted-block-device")
	if err := syscall.Mknod(deviceNode, syscall.S_IFBLK|0o600, 8<<8|1); err != nil {
		t.Skipf("creating a block device node is not permitted: %v", err)
	}

	oldSysDevBlockDir := sysDevBlockDir
	oldDeviceDir := deviceDir
	sysDevBlockDir = devBlock
	deviceDir = "/dev"
	t.Cleanup(func() {
		sysDevBlockDir = oldSysDevBlockDir
		deviceDir = oldDeviceDir
	})

	path, err := mountedBlockDevicePath(deviceNode)
	if err != nil {
		t.Fatal(err)
	}
	if path != "/dev/sda" {
		t.Fatalf("mountedBlockDevicePath() = %q, want /dev/sda", path)
	}
}
