//go:build !windows

package whspool

import (
	"fmt"
	"syscall"
)

// Volume is the filesystem a spool directory is on (#2148): its size and the
// bytes an unprivileged writer can still use. The spool refuses deliveries at
// its own max_bytes, but a volume shared with anything else can fill first.
type Volume struct {
	Total uint64
	Free  uint64
}

// VolumeOf reads the filesystem dir is on.
func VolumeOf(dir string) (Volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return Volume{}, fmt.Errorf("reading the spool volume: %w", err)
	}
	bsize := uint64(st.Bsize) //nolint:gosec,unconvert // a block size is positive; its type differs by platform
	return Volume{Total: st.Blocks * bsize, Free: st.Bavail * bsize}, nil
}
