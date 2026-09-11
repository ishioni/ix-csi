//go:build !linux

package driver

import "fmt"

func mountedBlockDevicePath(path string) (string, error) {
	return "", fmt.Errorf("block-device mount discovery is only supported on Linux")
}
