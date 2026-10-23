package caldav

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/emersion/go-ical"

	"github.com/txn2/mcp-data-platform/internal/untrusted"
)

// Event is one occurrence of a calendar event as a tool returns it: the
// platform's reading of when it is and what state it is in, and, under
// External, everything the event's author wrote. Start and End are UTC; the
// zone the event was written in is TimeZone, so a reader can say "10:00 in
// Berlin" without converting back.
type Event struct {
	UID      string `json:"uid"`
	Calendar string `json:"calendar,omitempty"`
	Href     string `json:"href,omitempty"`
	Start    string `json:"start"`
	End      string `json:"end"`
	// TimeZone is the IANA zone the start was written in, "UTC" for a UTC
	// time, and "floating" for a time with no zone (read in the
	// connection's default zone).
	TimeZone string `json:"time_zone"`
	// TimeZoneUnresolved is set when the event names a zone the platform
	// could not map to an IANA zone; its times were read as UTC.
	TimeZoneUnresolved string `json:"time_zone_unresolved,omitempty"`
	AllDay             bool   `json:"all_day"`
	// StartDate and EndDate are the dates of an all-day event, EndDate
	// exclusive as iCalendar writes it.
	StartDate string `json:"start_date,omitempty"`
	EndDate   string `json:"end_date,omitempty"`
	// Status is confirmed, tentative or canceled.
	Status string `json:"status"`
	// Recurring is set on every occurrence of a recurring event, and
	// RecurrenceID names which occurrence (its original start, UTC).
	Recurring    bool   `json:"recurring"`
	RecurrenceID string `json:"recurrence_id,omitempty"`
	LastModified string `json:"last_modified,omitempty"`
	// External is what the event's author wrote: anyone who can send an
	// invitation writes these fields (untrusted.Notice).
	External EventText `json:"external"`
	// Raw is the calendar object's iCalendar text, when the caller asked.
	Raw string `json:"raw,omitempty"`
}

// EventText is the author-written half of an event.
type EventText struct {
	Summary     string   `json:"summary"`
	Description string   `json:"description,omitempty"`
	Location    string   `json:"location,omitempty"`
	URL         string   `json:"url,omitempty"`
	Organizer   *Person  `json:"organizer,omitempty"`
	Attendees   []Person `json:"attendees"`
}

// Person is an organizer or attendee.
type Person struct {
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	// Role is chair, required, optional or non-participant.
	Role string `json:"role,omitempty"`
	// Participation is accepted, declined, tentative, delegated or
	// needs-action: the attendee's answer as the server holds it.
	Participation string `json:"participation,omitempty"`
}

// Window is a half-open range of time events are expanded into.
type Window struct {
	Start time.Time
	End   time.Time
}

// ExpandOptions shape how objects become events.
type ExpandOptions struct {
	// Floating is the zone a time written without one is read in. Nil is UTC.
	Floating *time.Location
	// Limit caps the occurrences returned; past it Expand reports truncated.
	Limit int
	// Raw keeps each object's iCalendar text on its events.
	Raw bool
}

// zoneUTC names a time written in UTC, or one read as UTC.
const zoneUTC = "UTC"

// maxDescriptionRunes bounds an event description; an invitation body pasted
// from a mail thread can be long, and the summary is what a reader needs.
const maxDescriptionRunes = 4000

// errNoEvents means an object holds no VEVENT.
var errNoEvents = errors.New("caldav: the calendar object holds no event")

// Expand reads every object into the occurrences that overlap w, recurring
// events expanded, newest instance overrides applied, sorted by start. An
// object that does not parse is skipped and named in skipped; truncated is set
// when Limit cut the list.
func Expand(objs []Object, calendar string, w Window, opts ExpandOptions) (events []Event, skipped []string, truncated bool) {
	events = []Event{}
	for _, o := range objs {
		evs, err := expandObject(o, calendar, w, opts)
		if err != nil {
			skipped = append(skipped, o.Href)
			continue
		}
		events = append(events, evs...)
	}
	slices.SortStableFunc(events, func(a, b Event) int { return strings.Compare(a.Start, b.Start) })
	if opts.Limit > 0 && len(events) > opts.Limit {
		return events[:opts.Limit], skipped, true
	}
	return events, skipped, false
}

// One reads a single object whole: every VEVENT in it, the recurring master
// and its overrides, without expanding recurrence.
func One(o Object, calendar string, opts ExpandOptions) ([]Event, error) {
	cal, zones, err := decode(o.Data)
	if err != nil {
		return nil, err
	}
	var out []Event
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		ev, err := readEvent(comp, zones, opts)
		if err != nil {
			return nil, err
		}
		ev.Recurring = comp.Props.Get(ical.PropRecurrenceRule) != nil || comp.Props.Get(ical.PropRecurrenceID) != nil
		ev.Calendar, ev.Href = calendar, o.Href
		if opts.Raw {
			ev.Raw = o.Data
		}
		out = append(out, ev)
	}
	if len(out) == 0 {
		return nil, errNoEvents
	}
	return out, nil
}

func decode(data string) (*ical.Calendar, *zoneResolver, error) {
	cal, err := ical.NewDecoder(strings.NewReader(data)).Decode()
	if err != nil {
		return nil, nil, fmt.Errorf("caldav: reading the calendar object: %w", err)
	}
	zones := newZoneResolver(cal)
	zones.normalize(cal.Component)
	return cal, zones, nil
}

func expandObject(o Object, calendar string, w Window, opts ExpandOptions) ([]Event, error) {
	cal, zones, err := decode(o.Data)
	if err != nil {
		return nil, err
	}
	parts, err := split(cal, opts.Floating)
	if err != nil {
		return nil, err
	}
	x := &expansion{zones: zones, w: w, opts: opts, overrides: parts.overrides, seen: map[int64]bool{}}
	for _, comp := range parts.singles {
		if err := x.keep(comp, false, ""); err != nil {
			return nil, err
		}
	}
	if parts.master != nil {
		err = x.master(parts.master)
	} else {
		// Overrides with no master in the object: a server that sent only
		// the instances it was asked about.
		err = x.unmatched()
	}
	if err != nil {
		return nil, err
	}
	for i := range x.out {
		x.out[i].Calendar, x.out[i].Href = calendar, o.Href
		if opts.Raw {
			x.out[i].Raw = o.Data
		}
	}
	return x.out, nil
}

// parts are an object's events by role: the recurring master, the instances
// written apart from its rule (by the original start's Unix time), and the
// events that do not recur.
type parts struct {
	master    *ical.Component
	overrides map[int64]*ical.Component
	singles   []*ical.Component
}

func split(cal *ical.Calendar, floating *time.Location) (parts, error) {
	p := parts{overrides: map[int64]*ical.Component{}}
	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}
		switch {
		case comp.Props.Get(ical.PropRecurrenceID) != nil:
			rid, err := comp.Props.DateTime(ical.PropRecurrenceID, floating)
			if err != nil {
				return p, fmt.Errorf("caldav: recurrence id: %w", err)
			}
			p.overrides[rid.Unix()] = comp
		case comp.Props.Get(ical.PropRecurrenceRule) != nil || comp.Props.Get(ical.PropRecurrenceDates) != nil:
			p.master = comp
		default:
			p.singles = append(p.singles, comp)
		}
	}
	return p, nil
}

// maxOccurrencesPerEvent bounds how many instances one rule is expanded to in
// one window: a rule that fires every minute does not fill a reply.
const maxOccurrencesPerEvent = 1000

// expansion collects one object's occurrences in a window.
type expansion struct {
	zones     *zoneResolver
	w         Window
	opts      ExpandOptions
	overrides map[int64]*ical.Component
	// seen are the overrides an occurrence of the rule already used.
	seen map[int64]bool
	out  []Event
}

// keep reads comp and keeps it when it overlaps the window; recurring marks
// an instance of a rule, recurrenceID the occurrence it stands for.
func (x *expansion) keep(comp *ical.Component, recurring bool, recurrenceID string) error {
	ev, err := readEvent(comp, x.zones, x.opts)
	if err != nil {
		return err
	}
	if recurring {
		ev.Recurring, ev.RecurrenceID = true, recurrenceID
	}
	x.add(ev)
	return nil
}

func (x *expansion) add(ev Event) {
	if overlaps(ev, x.w) {
		x.out = append(x.out, ev)
	}
}

// master expands a recurring event's rule in the window, each occurrence the
// master's or the override written for it, then the overrides moved into the
// window from an original start outside it.
func (x *expansion) master(master *ical.Component) error {
	base, err := readEvent(master, x.zones, x.opts)
	if err != nil {
		return err
	}
	start, end, err := bounds(master, x.opts.Floating)
	if err != nil {
		return err
	}
	dur := end.Sub(start)
	set, err := master.RecurrenceSet(locationOf(start))
	if err != nil {
		return fmt.Errorf("caldav: recurrence: %w", err)
	}
	var occ []time.Time
	if set != nil {
		occ = set.Between(x.w.Start.Add(-dur), x.w.End, true)
	}
	if len(occ) > maxOccurrencesPerEvent {
		occ = occ[:maxOccurrencesPerEvent]
	}
	for _, t := range occ {
		if err := x.occurrence(base, t, dur); err != nil {
			return err
		}
	}
	return x.unmatched()
}

// occurrence keeps one occurrence of the rule at t.
func (x *expansion) occurrence(base Event, t time.Time, dur time.Duration) error {
	x.seen[t.Unix()] = true
	if ov, ok := x.overrides[t.Unix()]; ok {
		return x.keep(ov, true, utc(t))
	}
	ev := base
	ev.External.Attendees = slices.Clone(base.External.Attendees)
	ev.Recurring, ev.RecurrenceID = true, utc(t)
	setTimes(&ev, t, t.Add(dur), base.AllDay)
	x.add(ev)
	return nil
}

// unmatched keeps the overrides no occurrence of the rule used.
func (x *expansion) unmatched() error {
	for rid, ov := range x.overrides {
		if x.seen[rid] {
			continue
		}
		if err := x.keep(ov, true, utc(time.Unix(rid, 0))); err != nil {
			return err
		}
	}
	return nil
}

func overlaps(ev Event, w Window) bool {
	s, err1 := time.Parse(time.RFC3339, ev.Start)
	e, err2 := time.Parse(time.RFC3339, ev.End)
	if err1 != nil || err2 != nil {
		return false
	}
	if e.Equal(s) {
		// An instant event overlaps when it falls inside the window.
		return !s.Before(w.Start) && s.Before(w.End)
	}
	return s.Before(w.End) && e.After(w.Start)
}

func utc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func locationOf(t time.Time) *time.Location {
	if t.Location() == nil {
		return time.UTC
	}
	return t.Location()
}

// bounds reads DTSTART and the end: DTEND, else DTSTART+DURATION, else one day
// for a date and the start itself for a date-time (RFC 5545 3.6.1).
func bounds(comp *ical.Component, floating *time.Location) (start, end time.Time, err error) {
	start, err = comp.Props.DateTime(ical.PropDateTimeStart, floating)
	if err != nil {
		return start, end, fmt.Errorf("caldav: start: %w", err)
	}
	if start.IsZero() {
		return start, end, errors.New("caldav: the event has no start")
	}
	if p := comp.Props.Get(ical.PropDateTimeEnd); p != nil {
		end, err = p.DateTime(floating)
		if err != nil {
			return start, end, fmt.Errorf("caldav: end: %w", err)
		}
		return start, end, nil
	}
	if p := comp.Props.Get(ical.PropDuration); p != nil {
		d, err := p.Duration()
		if err != nil {
			return start, end, fmt.Errorf("caldav: duration: %w", err)
		}
		return start, start.Add(d), nil
	}
	if isDate(comp.Props.Get(ical.PropDateTimeStart)) {
		return start, start.AddDate(0, 0, 1), nil
	}
	return start, start, nil
}

func isDate(p *ical.Prop) bool {
	if p == nil {
		return false
	}
	return p.ValueType() == ical.ValueDate || (p.Params.Get(ical.ParamValue) == "" && len(p.Value) == len("20060102"))
}

func readEvent(comp *ical.Component, zones *zoneResolver, opts ExpandOptions) (Event, error) {
	start, end, err := bounds(comp, opts.Floating)
	if err != nil {
		return Event{}, err
	}
	dtstart := comp.Props.Get(ical.PropDateTimeStart)
	ev := Event{
		UID:    untrusted.Line(text(comp, ical.PropUID)),
		Status: status(comp),
		External: EventText{
			Summary:     untrusted.Line(text(comp, ical.PropSummary)),
			Description: truncateRunes(untrusted.Clean(text(comp, ical.PropDescription)), maxDescriptionRunes),
			Location:    untrusted.Line(text(comp, ical.PropLocation)),
			URL:         untrusted.Line(rawValue(comp, ical.PropURL)),
			Organizer:   organizer(comp),
			Attendees:   attendees(comp),
		},
	}
	ev.AllDay = isDate(dtstart)
	ev.TimeZone = zoneName(dtstart, opts.Floating)
	if bad := zones.unresolved(dtstart); bad != "" {
		ev.TimeZone, ev.TimeZoneUnresolved = zoneUTC, bad
	}
	setTimes(&ev, start, end, ev.AllDay)
	if lm := comp.Props.Get(ical.PropLastModified); lm != nil {
		if t, err := lm.DateTime(time.UTC); err == nil {
			ev.LastModified = utc(t)
		}
	}
	return ev, nil
}

func setTimes(ev *Event, start, end time.Time, allDay bool) {
	ev.Start, ev.End = utc(start), utc(end)
	if allDay {
		ev.StartDate, ev.EndDate = start.Format(time.DateOnly), end.Format(time.DateOnly)
	}
}

func zoneName(p *ical.Prop, floating *time.Location) string {
	if p == nil {
		return zoneUTC
	}
	if tz := p.Params.Get(ical.PropTimezoneID); tz != "" {
		return tz
	}
	if strings.HasSuffix(p.Value, "Z") || isDate(p) {
		return zoneUTC
	}
	if floating != nil && floating != time.UTC {
		return "floating (" + floating.String() + ")"
	}
	return "floating"
}

func text(comp *ical.Component, name string) string {
	p := comp.Props.Get(name)
	if p == nil {
		return ""
	}
	s, err := p.Text()
	if err != nil {
		return p.Value
	}
	return s
}

func rawValue(comp *ical.Component, name string) string {
	if p := comp.Props.Get(name); p != nil {
		return p.Value
	}
	return ""
}

func status(comp *ical.Component) string {
	switch strings.ToUpper(rawValue(comp, ical.PropStatus)) {
	case "CANCELLED": //nolint:misspell // RFC 5545 spells the status value this way
		return "canceled"
	case "TENTATIVE":
		return "tentative"
	default:
		return "confirmed"
	}
}

func person(p ical.Prop) Person {
	return Person{
		Email:         untrusted.Line(strings.TrimPrefix(strings.TrimPrefix(p.Value, "mailto:"), "MAILTO:")),
		Name:          untrusted.Line(p.Params.Get(ical.ParamCommonName)),
		Role:          roles[strings.ToUpper(p.Params.Get(ical.ParamRole))],
		Participation: strings.ToLower(p.Params.Get(ical.ParamParticipationStatus)),
	}
}

// roles names the iCalendar ROLE values; absent is REQ-PARTICIPANT, as RFC
// 5545 3.2.16 defaults it.
var roles = map[string]string{ //nolint:gochecknoglobals // fixed protocol table
	"":                "required",
	"REQ-PARTICIPANT": "required",
	"OPT-PARTICIPANT": "optional",
	"CHAIR":           "chair",
	"NON-PARTICIPANT": "non-participant",
}

func organizer(comp *ical.Component) *Person {
	p := comp.Props.Get(ical.PropOrganizer)
	if p == nil {
		return nil
	}
	o := person(*p)
	o.Role, o.Participation = "", ""
	return &o
}

// maxAttendees bounds the attendee list one event carries.
const maxAttendees = 200

func attendees(comp *ical.Component) []Person {
	props := comp.Props.Values(ical.PropAttendee)
	out := make([]Person, 0, len(props))
	for i, p := range props {
		if i == maxAttendees {
			break
		}
		out = append(out, person(p))
	}
	return out
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
