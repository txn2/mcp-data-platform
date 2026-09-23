//go:build unix

package procload

import (
	"syscall"
	"time"
)

// processCPUTime is the user and system CPU time this process has used, and
// whether the operating system answered.
func processCPUTime() (time.Duration, bool) {
	var ru syscall.Rusage
	err := syscall.Getrusage(syscall.RUSAGE_SELF, &ru)
	return time.Duration(ru.Utime.Nano() + ru.Stime.Nano()), err == nil
}
