package scriptrun

import (
	"fmt"
	"strings"
	"time"

	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// timeLayout is the wire form of an instant (the run's fire time). Dates use
// script.DateLayout, so a script that slices a date out of the fire time and a
// schedule that binds one are talking about the same strings.
const timeLayout = time.RFC3339

// dateModule is the curated date arithmetic a report script actually needs, and
// nothing else. Every function is a pure transformation of its arguments.
//
// There is deliberately NO now() and no today(). A clock read is the single
// largest source of nondeterminism available to a script, and it is also
// unnecessary: the fire time is pinned onto run.fire_time when the run is
// created, so "yesterday" in a daily report is computed from a value recorded in
// the run record and the run reproduces exactly, months later, from that record.
// Adding now() would silently retire the determinism contract.
//
// Dates are YYYY-MM-DD strings rather than an opaque object type. A date is
// crossing into SQL and into an output file either way, the string form is what
// an author reads in a log, and a string needs no marshaling contract of its
// own.
var dateModule = &starlarkstruct.Module{
	Name: "date",
	Members: starlark.StringDict{
		"of":         starlark.NewBuiltin("date.of", dateOf),
		"parse":      starlark.NewBuiltin("date.parse", dateParse),
		"format":     starlark.NewBuiltin("date.format", dateFormat),
		"add_days":   starlark.NewBuiltin("date.add_days", dateAddDays),
		"add_months": starlark.NewBuiltin("date.add_months", dateAddMonths),
		"diff_days":  starlark.NewBuiltin("date.diff_days", dateDiffDays),
		"start_of_month": starlark.NewBuiltin("date.start_of_month",
			dateStartOfMonth),
		"weekday": starlark.NewBuiltin("date.weekday", dateWeekday),
	},
}

// argValue is the argument name every date function that takes a single date
// shares, spelled once so the three-place callers and unpackValue agree.
const argValue = "value"

// unpackValue reads the single `value` argument of a one-date function.
func unpackValue(b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (string, error) {
	var value string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, argValue, &value); err != nil {
		return "", argErr(b, err)
	}
	return value, nil
}

// weekdayNames maps a Go weekday to the lowercase name date.weekday reports.
var weekdayNames = [...]string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

// parseDate reads a YYYY-MM-DD date in UTC.
func parseDate(name, value string) (time.Time, error) {
	t, err := time.ParseInLocation(script.DateLayout, value, time.UTC)
	if err != nil {
		return time.Time{}, fmt.Errorf("in %s: %q is not a date in YYYY-MM-DD form", name, value)
	}
	return t, nil
}

// dateOf extracts the calendar date from an instant such as run.fire_time,
// which is the intended bridge from the run's pinned time into date arithmetic.
func dateOf(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	value, err := unpackValue(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	t, err := time.Parse(timeLayout, value)
	if err != nil {
		// A caller who already holds a date should get it back unchanged rather
		// than an error telling them to convert what is already converted.
		if d, dErr := parseDate(b.Name(), value); dErr == nil {
			return starlark.String(d.Format(script.DateLayout)), nil
		}
		return nil, fmt.Errorf("in %s: %q is neither an RFC 3339 timestamp nor a YYYY-MM-DD date", b.Name(), value)
	}
	return starlark.String(t.UTC().Format(script.DateLayout)), nil
}

// dateParse validates a date and returns it normalized.
func dateParse(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	value, err := unpackValue(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	t, err := parseDate(b.Name(), value)
	if err != nil {
		return nil, err
	}
	return starlark.String(t.Format(script.DateLayout)), nil
}

// dateFormat renders a date through a small token vocabulary — YYYY, MM, DD —
// rather than exposing Go's reference-time layouts, which are folklore an agent
// should not have to have learned.
func dateFormat(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var value, layout string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, argValue, &value, "layout", &layout); err != nil {
		return nil, argErr(b, err)
	}
	t, err := parseDate(b.Name(), value)
	if err != nil {
		return nil, err
	}
	rendered := strings.NewReplacer(
		"YYYY", t.Format("2006"),
		"MM", t.Format("01"),
		"DD", t.Format("02"),
	).Replace(layout)
	return starlark.String(rendered), nil
}

// dateAddDays shifts a date by whole days, forward or back.
func dateAddDays(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		value string
		days  int
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, argValue, &value, "days", &days); err != nil {
		return nil, argErr(b, err)
	}
	t, err := parseDate(b.Name(), value)
	if err != nil {
		return nil, err
	}
	return starlark.String(t.AddDate(0, 0, days).Format(script.DateLayout)), nil
}

// dateAddMonths shifts a date by whole months. It follows Go's normalizing
// AddDate, so 31 January plus one month is 3 March in a non-leap year; a report
// that means "the same day next month" should say so with add_days, and one
// that means a month boundary should use start_of_month.
func dateAddMonths(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var (
		value  string
		months int
	)
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, argValue, &value, "months", &months); err != nil {
		return nil, argErr(b, err)
	}
	t, err := parseDate(b.Name(), value)
	if err != nil {
		return nil, err
	}
	return starlark.String(t.AddDate(0, months, 0).Format(script.DateLayout)), nil
}

// dateDiffDays returns the whole days from start to end; negative when end
// precedes start.
func dateDiffDays(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	var start, end string
	if err := starlark.UnpackArgs(b.Name(), args, kwargs, "start", &start, "end", &end); err != nil {
		return nil, argErr(b, err)
	}
	s, err := parseDate(b.Name(), start)
	if err != nil {
		return nil, err
	}
	e, err := parseDate(b.Name(), end)
	if err != nil {
		return nil, err
	}
	return starlark.MakeInt(int(e.Sub(s).Hours() / 24)), nil
}

// dateStartOfMonth returns the first day of the date's month.
func dateStartOfMonth(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	value, err := unpackValue(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	t, err := parseDate(b.Name(), value)
	if err != nil {
		return nil, err
	}
	return starlark.String(time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC).Format(script.DateLayout)), nil
}

// dateWeekday returns the lowercase weekday name, for the "skip weekends" rule
// every business-day report carries.
func dateWeekday(_ *starlark.Thread, b *starlark.Builtin, args starlark.Tuple, kwargs []starlark.Tuple) (starlark.Value, error) {
	value, err := unpackValue(b, args, kwargs)
	if err != nil {
		return nil, err
	}
	t, err := parseDate(b.Name(), value)
	if err != nil {
		return nil, err
	}
	return starlark.String(weekdayNames[int(t.Weekday())]), nil
}
