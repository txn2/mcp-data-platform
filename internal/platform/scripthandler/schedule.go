package scripthandler

import (
	"context"
	"fmt"
	"reflect"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// errNoSchedules is a deployment that cannot store a schedule.
var errNoSchedules = fmt.Errorf("this deployment cannot store schedules: %w", ErrInvalid)

// setSchedule adds a schedule to the script, or replaces the one req names.
// A schedule a script already holds with the same cadence and arguments is
// refused, naming it: a repeated call would otherwise double the runs.
func (s *Service) setSchedule(ctx context.Context, req SetRequest) (map[string]any, error) {
	if s.Schedules == nil {
		return nil, errNoSchedules
	}
	sc := req.Script
	held, err := s.Schedules.ListSchedules(ctx, script.ScheduleFilter{ScriptID: sc.ID})
	if err != nil {
		return nil, storeErr(err)
	}
	prev := heldSchedule(held, req.Handler)
	sched, err := script.BuildSchedule(sc, prev, script.ScheduleRequest{
		CronSpec: req.Cron, Timezone: req.Timezone, Params: req.Args, Actor: req.Email,
	}, s.Now())
	if err != nil {
		return nil, invalid(err)
	}
	if prev == nil {
		if err := checkNewSchedule(sc, held, sched); err != nil {
			return nil, err
		}
	}
	if err := s.Schedules.SetSchedule(ctx, sched); err != nil {
		return nil, storeOr(err)
	}
	out := scheduleFields(sc, sched)
	out["message"] = scheduleNote(sc, sched)
	return out, nil
}

// heldSchedule is the schedule in held with the id, nil for none.
func heldSchedule(held []script.Schedule, id string) *script.Schedule {
	if id == "" {
		return nil
	}
	for i := range held {
		if held[i].ID == id {
			return &held[i]
		}
	}
	return nil
}

// checkNewSchedule refuses adding sched to a script that holds the most
// schedules a script may, or one on its cadence with its arguments.
func checkNewSchedule(sc *script.Script, held []script.Schedule, sched *script.Schedule) error {
	if len(held) >= MaxPerKind {
		return fmt.Errorf("script %s already has %d schedule handlers, the most a script may hold: %w", sc.Name, len(held), ErrNotAllowed)
	}
	if dup := sameSchedule(held, sched); dup != nil {
		return fmt.Errorf("script %s already has schedule handler %s on %s (%s) with these args: %w",
			sc.Name, dup.ID, dup.CronSpec, dup.Timezone, ErrInvalid)
	}
	return nil
}

// SameSchedule reports whether the schedule handler id fires on the cadence
// and with the arguments given, as an import matches a bundle's schedule to
// one the script already holds.
func (s *Service) SameSchedule(ctx context.Context, id, cron, timezone string, args map[string]any) (bool, error) {
	if s.Schedules == nil {
		return false, errNoSchedules
	}
	sched, err := s.Schedules.ScheduleByID(ctx, id)
	if err != nil {
		return false, storeOr(err)
	}
	if timezone == "" {
		timezone = script.DefaultTimezone
	}
	return sched.CronSpec == cron && sched.Timezone == timezone && sameArgs(sched.Params, args), nil
}

// sameSchedule is the schedule in held with sched's cadence and arguments.
func sameSchedule(held []script.Schedule, sched *script.Schedule) *script.Schedule {
	for i := range held {
		h := &held[i]
		if h.CronSpec == sched.CronSpec && h.Timezone == sched.Timezone && sameArgs(h.Params, sched.Params) {
			return h
		}
	}
	return nil
}

func sameArgs(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

// scheduleFields is one schedule handler as the commands report it.
func scheduleFields(sc *script.Script, sched *script.Schedule) map[string]any {
	out := map[string]any{
		fieldHandler: sched.ID, fieldKind: KindSchedule, fieldName: sc.Name,
		"cron":     sched.CronSpec,
		"timezone": sched.Timezone,
		"args":     orEmptyArgs(sched.Params),
		"enabled":  sched.Enabled,
		// missed_fires is surfaced because it is the only place a gap shows.
		// The misfire policy collapses a run of missed fires into one run, so
		// the count is what tells an owner the automation was not running.
		"missed_fires": sched.MissedFires,
		"created_by":   sched.CreatedBy,
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

func orEmptyArgs(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// scheduleNote states plainly what the schedule will and will not do, so an
// author is never left waiting on a schedule attached to a script nothing may
// execute.
func scheduleNote(sc *script.Script, sched *script.Schedule) string {
	switch {
	case script.RefuseRun(sc) != nil:
		return "The schedule is saved, but nothing will execute this script: " + script.RefuseRun(sc).Error() + "."
	case !sched.Enabled:
		return "The schedule is saved and disabled; enable it with command=handler_enable."
	default:
		return "The platform will run the latest saved version on this cadence, with these parameters, as the script's own principal presenting your captured roles."
	}
}
