package levelalert

import (
	"context"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// Level is how bad a condition is.
type Level string

// The levels a condition is reported at.
const (
	Warning  Level = "warning"
	Critical Level = "critical"
)

// rank orders the levels; an unknown level ranks below warning.
func (l Level) rank() int {
	switch l {
	case Critical:
		return 2
	case Warning:
		return 1
	default:
		return 0
	}
}

// Worse returns the more severe of a and b; empty when neither is a level.
func Worse(a, b Level) Level {
	if b.rank() > a.rank() {
		return b
	}
	return a
}

// Thresholds are the two values a reading is judged against. A zero
// threshold is off. Below marks a reading that is worse the lower it is (free
// space, days left); otherwise higher is worse.
type Thresholds struct {
	Warning  float64 `json:"warning"`
	Critical float64 `json:"critical"`
	Below    bool    `json:"-"`
}

// Of returns the level v is at, or "" when it is under both thresholds.
func (t Thresholds) Of(v float64) Level {
	crossed := func(limit float64) bool {
		if limit == 0 {
			return false
		}
		if t.Below {
			return v <= limit
		}
		return v >= limit
	}
	switch {
	case crossed(t.Critical):
		return Critical
	case crossed(t.Warning):
		return Warning
	default:
		return ""
	}
}

// Finding is one condition at warning or critical.
type Finding struct {
	// Key names the condition and what it is about, unique within its
	// family: webhook:spool_age:replica-a. It never carries the level.
	Key   string
	Level Level
	// Title is the notification's one line: what, where, how bad.
	Title string
	// Body is markdown: the value, the threshold, and what to look at.
	Body string
	// Link is the admin page the condition is seen on.
	Link string
}

// Sender queues an alert for notification channels. notifyalert.Sender
// satisfies it.
type Sender interface {
	Send(ctx context.Context, names []string, key string, p notification.Payload) int
}
