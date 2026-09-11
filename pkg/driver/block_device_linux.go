//go:build linux

package driver

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func mountedBlockDevicePath(path string) (string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeDevice == 0 || info.Mode()&os.ModeCharDevice != 0 {
		return "", fmt.Errorf("%s is not a block device", path)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", fmt.Errorf("cannot read device number for %s", path)
	}
	dev := uint64(stat.Rdev)
	major := (dev>>8)&0xfff | (dev>>32)&0xfffff000
	minor := dev&0xff | (dev>>12)&0xffffff00
	link := filepath.Join(sysDevBlockDir, fmt.Sprintf("%d:%d", major, minor))
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", fmt.Errorf("failed to resolve device number %d:%d: %w", major, minor, err)
	}
	return filepath.Join(deviceDir, filepath.Base(resolved)), nil
}
