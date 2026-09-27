package runstate

import (
	"context"
	"time"
)

// FailureStreak is how a script's recent finished runs stand (#1934, #1935):
// how many in a row failed, how many of those failed the same way, and when
// it last succeeded. A pending, running or canceled run is not counted either
// way.
type FailureStreak struct {
	// Failed is how many of the most recent finished runs failed in a row,
	// newest first, up to the window the store reads; zero when the newest
	// finished run succeeded.
	Failed int
	// SameError is how many of those, newest first, ended on the same last
	// error line as the newest one. A failure that repeats word for word is
	// one the next run is unlikely to get past on its own.
	SameError int
	// LastError is the newest failure's last error line, which names what
	// went wrong: "Error in fail: fail: NWS returned 500".
	LastError string
	// LastFailedRunID, LastFailedVersion, LastFailedAt and LastCause describe
	// the newest failed run, when the streak holds one: the run a reader opens
	// to see why, the version it ran, and the cause it was recorded under.
	LastFailedRunID   string
	LastFailedVersion int
	LastFailedAt      *time.Time
	LastCause         string
	// LastSuccessAt is when the script's most recent successful run finished,
	// nil when none has.
	LastSuccessAt *time.Time
}

// FailureStreakReader reads the failure streak of each of a set of scripts.
// A script with no finished run is absent from the map.
type FailureStreakReader interface {
	FailureStreaks(ctx context.Context, scriptIDs []string) (map[string]FailureStreak, error)
}
