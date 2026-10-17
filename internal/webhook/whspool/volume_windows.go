//go:build windows

package whspool

import "errors"

// Volume is the filesystem a spool directory is on (#2148).
type Volume struct {
	Total uint64
	Free  uint64
}

// errNoVolume is VolumeOf's answer where the platform does not read one.
var errNoVolume = errors.New("reading the spool volume is not supported on windows")

// VolumeOf is not read on windows; the spool state reports no volume.
func VolumeOf(string) (Volume, error) { return Volume{}, errNoVolume }
