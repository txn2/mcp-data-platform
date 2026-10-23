package caldav

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/internal/untrusted"
	"github.com/txn2/mcp-data-platform/pkg/toolkit"
)

// ListInput is calendar_list's arguments.
type ListInput struct {
	Connection string `json:"connection,omitempty" jsonschema:"The caldav connection. Optional when the deployment has one."`
}

// CalendarInfo is one calendar as calendar_list reports it.
type CalendarInfo struct {
	Name        string   `json:"name"`
	Href        string   `json:"href"`
	Description string   `json:"description,omitempty"`
	Color       string   `json:"color,omitempty"`
	Components  []string `json:"components"`
}

// ListOutput is calendar_list's result.
type ListOutput struct {
	Connection string         `json:"connection"`
	Calendars  []CalendarInfo `json:"calendars"`
}

func (t *Toolkit) handleList(ctx context.Context, _ *mcp.CallToolRequest, in ListInput) (*mcp.CallToolResult, any, error) {
	c, err := t.conns.Get(ctx, in.Connection)
	if err != nil {
		return toolkit.ErrorResult(err.Error()), nil, nil
	}
	cals, err := c.discover(ctx, false)
	if err != nil {
		return toolkit.ErrorResult(fmt.Sprintf("caldav: reading the calendars of %s: %v", c.name, err)), nil, nil
	}
	out := ListOutput{Connection: c.name, Calendars: make([]CalendarInfo, 0, len(cals))}
	for _, cal := range cals {
		out.Calendars = append(out.Calendars, CalendarInfo{
			Name:        untrusted.Line(cal.DisplayName),
			Href:        cal.Href,
			Description: untrusted.Clean(cal.Description),
			Color:       cal.Color,
			Components:  cal.Components,
		})
	}
	return toolkit.JSONResult(out), out, nil
}
