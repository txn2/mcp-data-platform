package caldav

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/caldav"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// slash separates path segments.
const slash = "/"

// EventsInput is calendar_events' arguments.
type EventsInput struct {
	Connection string `json:"connection,omitempty" jsonschema:"The caldav connection. Optional when the deployment has one."`
	Calendar   string `json:"calendar,omitempty" jsonschema:"A calendar's name or href from calendar_list. Empty reads every calendar that holds events."`
	Start      string `json:"start,omitempty" jsonschema:"Start of the range: an RFC 3339 time or a YYYY-MM-DD date (midnight in the connection's time zone). Defaults to now."`
	End        string `json:"end,omitempty" jsonschema:"End of the range, exclusive, in the same forms. Defaults to seven days after start."`
	IncludeRaw bool   `json:"include_raw,omitempty" jsonschema:"Also return each event's iCalendar text."`
}

// EventsOutput is calendar_events' result.
type EventsOutput struct {
	Connection string         `json:"connection"`
	Start      string         `json:"start"`
	End        string         `json:"end"`
	Calendars  []string       `json:"calendars"`
	Events     []caldav.Event `json:"events"`
	// Truncated is set when the connection's max_events cut the list; ask
	// for a shorter range.
	Truncated bool `json:"truncated"`
	// Skipped are the calendar objects that did not parse as iCalendar.
	Skipped []string `json:"skipped,omitempty"`
	Notice  string   `json:"notice"`
}

// EventInput is calendar_event's arguments.
type EventInput struct {
	Connection string `json:"connection,omitempty" jsonschema:"The caldav connection. Optional when the deployment has one."`
	Href       string `json:"href" jsonschema:"The event's href, as calendar_events reports it."`
	IncludeRaw bool   `json:"include_raw,omitempty" jsonschema:"Also return the iCalendar text."`
}

// EventOutput is calendar_event's result: the event, and for a recurring
// event every instance written apart from its rule.
type EventOutput struct {
	Connection string         `json:"connection"`
	Href       string         `json:"href"`
	Events     []caldav.Event `json:"events"`
	Notice     string         `json:"notice"`
}

// RegisterTools registers the calendar tools.
func (t *Toolkit) RegisterTools(s *mcp.Server) {
	toolkit.AddTool(s, &mcp.Tool{
		Name:  ToolList,
		Title: "List Calendars",
		Description: "List the calendars on a CalDAV connection's account: each calendar's name, href, " +
			"color and the component types it holds. Pass a name or href to calendar_events.",
		Annotations: toolkit.ReadOnlyAnnotations(),
	}, t.handleList)
	toolkit.AddTool(s, &mcp.Tool{
		Name:  ToolEvents,
		Title: "List Calendar Events",
		Description: "List the events in a time range on one calendar or every calendar of a CalDAV connection, " +
			"sorted by start. Recurring events are expanded into their occurrences, with moved and canceled " +
			"instances applied. Each event has its start and end in UTC, the zone it was written in, an all-day " +
			"flag, its status (confirmed, tentative, canceled), and under \"external\" what its author wrote: " +
			"summary, description, location, organizer and attendees with their answers. " + untrusted.Notice,
		Annotations: toolkit.ReadOnlyAnnotations(),
	}, t.handleEvents)
	toolkit.AddTool(s, &mcp.Tool{
		Name:  ToolEvent,
		Title: "Read Calendar Event",
		Description: "Read one event of a CalDAV connection by the href calendar_events reported: the event, and " +
			"for a recurring event its rule's master and every instance written apart from it. " + untrusted.Notice,
		Annotations: toolkit.ReadOnlyAnnotations(),
	}, t.handleEvent)
}

func (t *Toolkit) handleEvents(ctx context.Context, _ *mcp.CallToolRequest, in EventsInput) (*mcp.CallToolResult, any, error) {
	c, err := t.conns.Get(ctx, in.Connection)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	w, err := c.window(in.Start, in.End, time.Now())
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	cals, err := c.chosen(ctx, in.Calendar)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	out := EventsOutput{
		Connection: c.name, Start: w.Start.UTC().Format(time.RFC3339), End: w.End.UTC().Format(time.RFC3339),
		Calendars: []string{}, Events: []caldav.Event{}, Notice: untrusted.Notice,
	}
	opts := caldav.ExpandOptions{Floating: c.cfg.Location(), Limit: c.cfg.MaxEvents, Raw: in.IncludeRaw}
	for _, cal := range cals {
		objs, err := c.client.Query(ctx, cal.Href, w.Start, w.End)
		if err != nil {
			return toolkit.ErrorResult(fmt.Sprintf("caldav: reading %s on %s: %v", cal.DisplayName, c.name, err)), nil, nil
		}
		evs, skipped, cut := caldav.Expand(objs, cal.Href, w, opts)
		out.Calendars = append(out.Calendars, untrusted.Line(cal.DisplayName))
		out.Events = append(out.Events, evs...)
		out.Skipped = append(out.Skipped, skipped...)
		out.Truncated = out.Truncated || cut
	}
	if len(cals) > 1 {
		out.Events, out.Truncated = mergeSorted(out.Events, c.cfg.MaxEvents, out.Truncated)
	}
	return toolkit.JSONResult(out), out, nil
}

func (t *Toolkit) handleEvent(ctx context.Context, _ *mcp.CallToolRequest, in EventInput) (*mcp.CallToolResult, any, error) {
	c, err := t.conns.Get(ctx, in.Connection)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	if strings.TrimSpace(in.Href) == "" {
		return toolkit.ErrorResult("caldav: href is required; calendar_events reports each event's href"), nil, nil
	}
	obj, err := c.client.Get(ctx, in.Href)
	if err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("caldav: reading %s on %s: %v", in.Href, c.name, err)), nil, nil
	}
	evs, err := caldav.One(obj, calendarOf(in.Href), caldav.ExpandOptions{Floating: c.cfg.Location(), Raw: in.IncludeRaw})
	if err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("caldav: %s on %s: %v", in.Href, c.name, err)), nil, nil
	}
	out := EventOutput{Connection: c.name, Href: in.Href, Events: evs, Notice: untrusted.Notice}
	return toolkit.JSONResult(out), out, nil
}

// window reads a call's range: forms are an RFC 3339 time or a date, which is
// midnight in the connection's zone. A missing start is now, a missing end
// seven days on; a range past max_range_days, or one that ends before it
// starts, is refused.
func (c *conn) window(start, end string, now time.Time) (caldav.Window, error) {
	loc := c.cfg.Location()
	s, err := parseBound(start, loc, now)
	if err != nil {
		return caldav.Window{}, fmt.Errorf("caldav: start: %w", err)
	}
	e, err := parseBound(end, loc, s.AddDate(0, 0, defaultRangeDays))
	if err != nil {
		return caldav.Window{}, fmt.Errorf("caldav: end: %w", err)
	}
	if !e.After(s) {
		return caldav.Window{}, fmt.Errorf("caldav: end (%s) must be after start (%s)", e.Format(time.RFC3339), s.Format(time.RFC3339))
	}
	if e.After(s.AddDate(0, 0, c.cfg.MaxRangeDays)) {
		return caldav.Window{}, fmt.Errorf("caldav: the range is longer than this connection's max_range_days (%d); ask for a shorter one", c.cfg.MaxRangeDays)
	}
	return caldav.Window{Start: s, End: e}, nil
}

func parseBound(v string, loc *time.Location, def time.Time) (time.Time, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return def, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, v, loc); err == nil {
		return t, nil
	}
	return time.Time{}, fmt.Errorf("value %q is neither an RFC 3339 time nor a YYYY-MM-DD date", v)
}

// chosen resolves the calendar a call names: by display name (any case), by
// href, or every calendar holding events when the name is empty.
func (c *conn) chosen(ctx context.Context, name string) ([]caldav.Calendar, error) {
	cals, err := c.discover(ctx, false)
	if err != nil {
		return nil, fmt.Errorf("caldav: reading the calendars of %s: %w", c.name, err)
	}
	name = strings.TrimSpace(name)
	var out []caldav.Calendar
	for _, cal := range cals {
		if name == "" {
			if holdsEvents(cal) {
				out = append(out, cal)
			}
			continue
		}
		if strings.EqualFold(cal.DisplayName, name) || strings.TrimSuffix(cal.Href, slash) == strings.TrimSuffix(name, slash) {
			return []caldav.Calendar{cal}, nil
		}
	}
	if name != "" {
		names := make([]string, 0, len(cals))
		for _, cal := range cals {
			names = append(names, cal.DisplayName)
		}
		return nil, fmt.Errorf("caldav: %s has no calendar %q; its calendars are %s", c.name, name, strings.Join(names, ", "))
	}
	return out, nil
}

func holdsEvents(cal caldav.Calendar) bool {
	for _, comp := range cal.Components {
		if strings.EqualFold(comp, "VEVENT") {
			return true
		}
	}
	return false
}

// mergeSorted orders events from several calendars by start and re-applies
// the cap the per-calendar expansion applied to each.
func mergeSorted(evs []caldav.Event, limit int, truncated bool) ([]caldav.Event, bool) {
	sortByStart(evs)
	if limit > 0 && len(evs) > limit {
		return evs[:limit], true
	}
	return evs, truncated
}

// calendarOf is the collection an object href is in.
func calendarOf(href string) string {
	if i := strings.LastIndex(strings.TrimSuffix(href, slash), slash); i >= 0 {
		return href[:i+1]
	}
	return ""
}

func sortByStart(evs []caldav.Event) {
	slices.SortStableFunc(evs, func(a, b caldav.Event) int { return strings.Compare(a.Start, b.Start) })
}
