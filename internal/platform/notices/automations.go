package notices

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/txn2/mcp-data-platform/internal/runstate"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// maxAutomationNotices bounds how many failing automations one briefing
// names; the rest are counted.
const maxAutomationNotices = 10

// AutomationSource is what the briefing reads a caller's automations from:
// their scripts, which of those are scheduled, and how each one's recent runs
// stand.
type AutomationSource interface {
	List(ctx context.Context, filter script.ListFilter) ([]script.Script, error)
	ListSchedules(ctx context.Context, filter script.ScheduleFilter) ([]script.Schedule, error)
	runstate.FailureStreakReader
}

// AutomationNotice is one automation the caller owns whose latest finished
// run failed (#1934). It is listed for as long as the automation keeps
// failing, because the problem it reports is not solved by having been told;
// New marks the ones whose latest failure arrived since the caller was last
// briefed.
type AutomationNotice struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	// Reference dereferences the automation, for `fetch`.
	Reference string `json:"reference"`
	// Version is the version the failed run executed.
	Version int `json:"version"`
	// RunID is the failed run, for manage_script get_run.
	RunID string `json:"run_id"`
	// Cause is why it failed (script, upstream, transient, memory, ...), and
	// Retryable whether running it again is expected to succeed.
	Cause     string `json:"cause"`
	Retryable bool   `json:"retryable"`
	// Error is the failure's last line, which names what went wrong.
	Error string `json:"error,omitempty"`
	// ConsecutiveFailures is how many finished runs in a row have failed.
	ConsecutiveFailures int `json:"consecutive_failures"`
	// FailedAt is when the failed run finished, RFC3339.
	FailedAt string `json:"failed_at,omitempty"`
	// LastSucceededAt is when a run of it last succeeded, RFC3339; absent
	// when none has.
	LastSucceededAt string `json:"last_succeeded_at,omitempty"`
	// Scheduled is whether a schedule is set to fire it again.
	Scheduled bool `json:"scheduled"`
	// New is whether it failed since the caller was last briefed.
	New bool `json:"new"`
}

// failingAutomations returns the automations the caller owns whose latest
// finished run failed, newest failure first, capped, with how many there are.
// An administrator is told about their own: every other owner is told about
// theirs.
func (h *Handle) failingAutomations(ctx context.Context, c caller, since time.Time) ([]AutomationNotice, int, error) {
	if h.scripts == nil || c.email == "" {
		return nil, 0, nil
	}
	o, err := h.ownedRuns(ctx, c.email)
	if err != nil {
		return nil, 0, err
	}
	var out []AutomationNotice
	for _, sc := range o.scripts {
		st, ok := o.streaks[sc.ID]
		if !ok || st.Failed == 0 {
			continue
		}
		out = append(out, automationNotice(sc, st, o.scheduled[sc.ID], since))
	}
	slices.SortStableFunc(out, func(a, b AutomationNotice) int { return cmp.Compare(b.FailedAt, a.FailedAt) })
	total := len(out)
	if total > maxAutomationNotices {
		out = out[:maxAutomationNotices]
	}
	return out, total, nil
}

// owned is the caller's enabled automations, how each one's recent runs
// stand, and which of them are scheduled.
type owned struct {
	scripts   []script.Script
	streaks   map[string]runstate.FailureStreak
	scheduled map[string]bool
}

// ownedRuns reads what owned holds for the caller.
func (h *Handle) ownedRuns(ctx context.Context, email string) (owned, error) {
	enabled := true
	scripts, err := h.scripts.List(ctx, script.ListFilter{OwnerEmail: email, Enabled: &enabled})
	if err != nil {
		return owned{}, fmt.Errorf("listing the caller's automations: %w", err)
	}
	if len(scripts) == 0 {
		return owned{}, nil
	}
	ids := make([]string, 0, len(scripts))
	for _, sc := range scripts {
		ids = append(ids, sc.ID)
	}
	streaks, err := h.scripts.FailureStreaks(ctx, ids)
	if err != nil {
		return owned{}, fmt.Errorf("reading the caller's automation runs: %w", err)
	}
	scheduled, err := h.scheduledOf(ctx, ids)
	if err != nil {
		return owned{}, err
	}
	return owned{scripts: scripts, streaks: streaks, scheduled: scheduled}, nil
}

// scheduledOf reports which of the scripts have an enabled schedule.
func (h *Handle) scheduledOf(ctx context.Context, ids []string) (map[string]bool, error) {
	schedules, err := h.scripts.ListSchedules(ctx, script.ScheduleFilter{ScriptIDs: ids})
	if err != nil {
		return nil, fmt.Errorf("reading the caller's automation schedules: %w", err)
	}
	out := make(map[string]bool, len(schedules))
	for _, s := range schedules {
		out[s.ScriptID] = s.Enabled
	}
	return out, nil
}

func automationNotice(sc script.Script, st runstate.FailureStreak, scheduled bool, since time.Time) AutomationNotice {
	n := AutomationNotice{
		Name: sc.Name, DisplayName: sc.DisplayName, Reference: reference("script", sc.ID),
		Version: st.LastFailedVersion, RunID: st.LastFailedRunID,
		Cause: st.LastCause, Retryable: runstate.CauseRetryable(st.LastCause),
		Error: st.LastError, ConsecutiveFailures: st.Failed, Scheduled: scheduled,
	}
	if st.LastFailedAt != nil {
		n.FailedAt = stamp(*st.LastFailedAt)
		n.New = st.LastFailedAt.After(since)
	}
	if st.LastSuccessAt != nil {
		n.LastSucceededAt = stamp(*st.LastSuccessAt)
	}
	return n
}
