//go:build !unix

package procload

import "time"

// processCPUTime reports that the process's CPU time is not read on this
// platform, which leaves CPU out of the run worker's admission.
func processCPUTime() (time.Duration, bool) { return 0, false }
