package scriptlayer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// schedulable resolves the script a schedule command names and checks the
// caller may set its cadence: its owner, or an administrator.
//
// It is deliberately NOT the edit rule. Editing changes what a script does and
// sends the result back through review, so it is confined to a personal script
// unless an admin makes it; a schedule changes only when an already-approved
// version runs and with which parameters, and the execution gate and the
// capability grant are re-read at every fire. Re-timing therefore cannot reach
// anything the script could not already reach, and there is no reason the owner
// of a global or persona script must ask an administrator to pause their own
// report.
func (h *Handle) schedulable(ctx context.Context, input manageScriptInput) (*script.Script, *mcp.CallToolResult) {
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		return nil, errResult
	}
	if !h.ownsScript(ctx, sc) {
		return nil, errorResult("only the owner of a script can change its schedule")
	}
	return sc, nil
}

// handleScheduleSet creates or replaces a script's schedule.
//
// It is an owner-or-admin action, not a reviewer's, and that is the whole
// authority story: a schedule says when an already-approved version runs and
// with which parameters, and can no more widen what that version reaches than
// calling run_script can. What executes and what it may touch were both decided
// at approval.
func (h *Handle) handleScheduleSet(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.schedulable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	if h.schedules == nil {
		return errorResult("this deployment cannot store schedules"), nil, nil
	}
	approved, errResult := h.approvedVersionOrNil(ctx, sc)
	if errResult != nil {
		return errResult, nil, nil
	}
	prev, err := h.existingSchedule(ctx, sc.ID)
	if err != nil {
		slog.Error("failed to read a script schedule", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to read the current schedule"), nil, nil
	}
	sched, err := script.BuildSchedule(sc, approved, prev, script.ScheduleRequest{
		CronSpec: input.Cron, Timezone: input.Timezone,
		Params: input.Args, Actor: resolveEmail(ctx),
	}, time.Now())
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if err := h.schedules.SetSchedule(ctx, sched); err != nil {
		slog.Error("failed to set a script schedule", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to set the schedule"), nil, nil
	}
	out := scheduleFields(sc, sched)
	out["message"] = scheduleNote(sc, sched)
	return jsonResult(out)
}

// approvedVersionOrNil loads the version a schedule's fires will execute, or
// nil when the script has none yet.
func (h *Handle) approvedVersionOrNil(ctx context.Context, sc *script.Script) (*script.Version, *mcp.CallToolResult) {
	if !sc.Executable() || h.versions == nil {
		return nil, nil
	}
	v, err := h.versions.GetVersionByID(ctx, sc.ApprovedVersionID)
	if err != nil {
		slog.Error("failed to read an approved script version", fieldName, sc.Name, logKeyError, err)
		return nil, errorResult("failed to read the approved version")
	}
	return v, nil
}

// existingSchedule reads the schedule being replaced, treating "there is none"
// as a normal outcome rather than a failure.
func (h *Handle) existingSchedule(ctx context.Context, scriptID string) (*script.Schedule, error) {
	sched, err := h.schedules.GetSchedule(ctx, scriptID)
	if errors.Is(err, script.ErrScheduleNotFound) {
		return nil, nil //nolint:nilnil // "no schedule yet" is the normal first-time case, not a failure
	}
	if err != nil {
		return nil, fmt.Errorf("reading the schedule: %w", err)
	}
	return sched, nil
}

// handleScheduleList returns the schedules of the scripts the caller may see.
func (h *Handle) handleScheduleList(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	if h.schedules == nil {
		return errorResult("this deployment cannot store schedules"), nil, nil
	}
	if input.Name != "" {
		return h.oneSchedule(ctx, input)
	}
	visible, err := h.visibleScripts(ctx, input.Limit)
	if err != nil {
		slog.Error("failed to list scripts", logKeyError, err)
		return errorResult("failed to list schedules"), nil, nil
	}
	// The visibility rule is pushed into the query rather than applied to the
	// answer. A non-admin listing every schedule and discarding the ones they
	// may not see would read the whole table to return their own handful, and
	// would then have to resolve each discarded row's script to know to discard
	// it. Admins are not scoped, which is the same rule the script listing uses.
	filter := script.ScheduleFilter{Limit: input.Limit}
	if !h.isAdminPersona(ctx) {
		filter.ScriptIDs = visibleIDs(visible)
	}
	schedules, err := h.schedules.ListSchedules(ctx, filter)
	if err != nil {
		slog.Error("failed to list script schedules", logKeyError, err)
		return errorResult("failed to list schedules"), nil, nil
	}
	items := []map[string]any{}
	for i := range schedules {
		// An admin's listing is unscoped, so a schedule may name a script the
		// bulk lookup's own limit cut off. It is resolved individually rather
		// than dropped, which would silently shorten a listing that reads as
		// complete.
		sc, ok := visible[schedules[i].ScriptID]
		if !ok {
			if sc = h.visibleScript(ctx, schedules[i].ScriptID); sc == nil {
				continue
			}
		}
		items = append(items, scheduleFields(sc, &schedules[i]))
	}
	return jsonResult(map[string]any{"schedules": items, "count": len(items)})
}

// visibleIDs is the script-id scope of a listing. It is never nil, because a
// nil filter means "no scope" and a caller who can see nothing must see no
// schedules rather than all of them.
func visibleIDs(visible map[string]*script.Script) []string {
	ids := make([]string, 0, len(visible))
	for id := range visible {
		ids = append(ids, id)
	}
	return ids
}

// visibleScript resolves one script by id and applies the visibility rule,
// returning nil when the caller may not see it or it could not be read.
func (h *Handle) visibleScript(ctx context.Context, scriptID string) *script.Script {
	sc, err := h.store.GetByID(ctx, scriptID)
	if err != nil || sc == nil {
		return nil
	}
	if h.isAdminPersona(ctx) || sc.VisibleTo(resolveEmail(ctx), personaName(ctx)) {
		return sc
	}
	return nil
}

// visibleScripts maps the ids of the scripts the caller may see to the scripts.
func (h *Handle) visibleScripts(ctx context.Context, limit int) (map[string]*script.Script, error) {
	filter := script.ListFilter{Limit: limit}
	if !h.isAdminPersona(ctx) {
		filter.VisibleTo, filter.VisiblePersona = resolveEmail(ctx), personaName(ctx)
	}
	scripts, err := h.store.List(ctx, filter)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller names the operation
	}
	out := make(map[string]*script.Script, len(scripts))
	for i := range scripts {
		out[scripts[i].ID] = &scripts[i]
	}
	return out, nil
}

// oneSchedule answers the listing for a single named script.
func (h *Handle) oneSchedule(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.readable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	sched, err := h.existingSchedule(ctx, sc.ID)
	if err != nil {
		slog.Error("failed to read a script schedule", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to read the schedule"), nil, nil
	}
	if sched == nil {
		return jsonResult(map[string]any{
			"schedules": []map[string]any{}, "count": 0,
			"message": "This script has no schedule. Set one with command=schedule_set.",
		})
	}
	return jsonResult(map[string]any{
		"schedules": []map[string]any{scheduleFields(sc, sched)}, "count": 1,
	})
}

// handleScheduleEnable turns a schedule on.
func (h *Handle) handleScheduleEnable(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.setScheduleEnabled(ctx, input, true)
}

// handleScheduleDisable turns a schedule off.
//
// Disabling is how a schedule is retired: there is deliberately no way to
// delete one, because a schedule that produced runs is part of the explanation
// of those runs.
func (h *Handle) handleScheduleDisable(ctx context.Context, input manageScriptInput) (*mcp.CallToolResult, any, error) {
	return h.setScheduleEnabled(ctx, input, false)
}

// setScheduleEnabled applies an enable/disable to the named script's schedule.
func (h *Handle) setScheduleEnabled(ctx context.Context, input manageScriptInput, enabled bool) (*mcp.CallToolResult, any, error) {
	sc, errResult := h.schedulable(ctx, input)
	if errResult != nil {
		return errResult, nil, nil
	}
	if h.schedules == nil {
		return errorResult("this deployment cannot store schedules"), nil, nil
	}
	if err := h.schedules.SetScheduleEnabled(ctx, sc.ID, enabled, resolveEmail(ctx)); err != nil {
		if errors.Is(err, script.ErrScheduleNotFound) {
			return errorResult("this script has no schedule; set one with command=schedule_set"), nil, nil
		}
		slog.Error("failed to change a script schedule", fieldName, sc.Name, logKeyError, err)
		return errorResult("failed to change the schedule"), nil, nil
	}
	sched, err := h.existingSchedule(ctx, sc.ID)
	if err != nil || sched == nil {
		// The change landed; only the read-back failed. Report the change.
		return jsonResult(map[string]any{fieldName: sc.Name, "enabled": enabled})
	}
	return jsonResult(scheduleFields(sc, sched))
}

// scheduleFields renders one schedule for a response.
func scheduleFields(sc *script.Script, sched *script.Schedule) map[string]any {
	out := map[string]any{
		fieldName:    sc.Name,
		"cron":       sched.CronSpec,
		"timezone":   sched.Timezone,
		"args":       sched.Params,
		"enabled":    sched.Enabled,
		"executable": sc.Executable(),
		// missed_fires is surfaced because it is the only place a gap shows.
		// The misfire policy collapses a run of missed fires into one run, so
		// the count is what tells an owner the automation was not running.
		"missed_fires": sched.MissedFires,
	}
	// DueAt rather than NextRunAt: a paused schedule's stored due time is the
	// fire it is parked on, and reporting it here would tell an author their
	// paused automation is about to run.
	if due := sched.DueAt(); !due.IsZero() {
		out["next_run_at"] = due.UTC()
	}
	if sched.LastFireAt != nil {
		out["last_fire_at"] = sched.LastFireAt.UTC()
	}
	return out
}

// scheduleNote states plainly what the schedule will and will not do, so an
// author is never left waiting on a schedule attached to a script nothing may
// execute.
func scheduleNote(sc *script.Script, sched *script.Schedule) string {
	switch {
	case !sc.Executable():
		return "The schedule is saved, but this script has no approved version, so nothing will execute it. Ask an administrator to review and approve a version."
	case !sched.Enabled:
		return "The schedule is saved and disabled; enable it with command=schedule_enable."
	default:
		return "The platform will run the approved version on this cadence, with these parameters, as the script's own principal under the capabilities its approval granted."
	}
}
