package apichange

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// SettingsSection is the platform_settings section holding where change
// reports and upstream warnings are sent.
const SettingsSection = "api_changes"

// Settings is where change reports and warnings go: notification channels
// by name, none by default.
type Settings struct {
	Channels []string `json:"channels"`
}

// Sender posts a document to named channels (levelalert.Sender, the
// notification substrate's channel sender).
type Sender interface {
	Send(ctx context.Context, names []string, key string, p notification.Payload) int
}

// ChannelNotifier sends change reports and new warnings to the channels the
// api_changes settings name. Nothing is sent while none are named.
type ChannelNotifier struct {
	Settings *settingsjson.Section[Settings]
	Sender   Sender
	// PortalBase is the portal's public address, for the link back.
	PortalBase string
}

// NewSettings is the api_changes section over db.
func NewSettings(db *sql.DB) *settingsjson.Section[Settings] {
	return settingsjson.New[Settings](db, SettingsSection)
}

// ReadSettings returns the stored settings, the defaults when none are.
func ReadSettings(ctx context.Context, s *settingsjson.Section[Settings]) (Settings, error) {
	stored, err := s.Get(ctx)
	if errors.Is(err, settingsjson.ErrNotFound) {
		return Settings{Channels: []string{}}, nil
	}
	if err != nil {
		return Settings{}, fmt.Errorf("reading the api change settings: %w", err)
	}
	if stored.Value.Channels == nil {
		stored.Value.Channels = []string{}
	}
	return stored.Value, nil
}

func (n *ChannelNotifier) channels(ctx context.Context) []string {
	if n == nil || n.Sender == nil || n.Settings == nil {
		return nil
	}
	s, err := ReadSettings(ctx, n.Settings)
	if err != nil {
		slog.WarnContext(ctx, "api changes: reading where to send", "error", logsan.SanitizeForLog(err.Error()))
		return nil
	}
	return s.Channels
}

// space separates the words of an operation line.
const space = " "

// maxNotified bounds the operations one message lists per section.
const maxNotified = 50

// Changed sends a change report.
func (n *ChannelNotifier) Changed(ctx context.Context, c Change) {
	names := n.channels(ctx)
	if len(names) == 0 {
		return
	}
	title := "API spec changed: " + c.CatalogID + "/" + c.SpecName
	link := strings.TrimRight(n.PortalBase, "/") + "/portal/admin/api-catalogs"
	n.Sender.Send(ctx, names, "api_change:"+c.ID, document(title, ChangeBody(c), link))
}

// Warned sends a new upstream warning.
func (n *ChannelNotifier) Warned(ctx context.Context, w Warning) {
	names := n.channels(ctx)
	if len(names) == 0 {
		return
	}
	title := "API upstream warning: " + w.Connection + " " + warningLabel(w.Kind)
	paragraphs := []string{fmt.Sprintf("%s on connection %s: %s", warningLabel(w.Kind), code(w.Connection), code(w.Operation))}
	if w.Detail != "" {
		paragraphs = append(paragraphs, code(w.Detail))
	}
	body := strings.Join(paragraphs, "\n\n")
	link := strings.TrimRight(n.PortalBase, "/") + "/portal/admin/connections"
	n.Sender.Send(ctx, names, "api_warning:"+w.Connection+":"+w.Operation+":"+w.Kind, document(title, body, link))
}

func document(title, body, link string) notification.Payload {
	return notification.Payload{
		Kind: notification.KindChannel, ItemTitle: title, Link: link,
		Document: &notification.Document{Title: title, Body: body, Link: link},
	}
}

// warningLabel is a warning kind as a person reads it.
func warningLabel(kind string) string {
	switch kind {
	case WarningDeprecation:
		return "Deprecation"
	case WarningSunset:
		return "Sunset"
	default:
		return "Removed"
	}
}

// statusLabel is a change status as a person reads it.
func statusLabel(status string) string {
	switch status {
	case StatusPending:
		return "Waiting for review"
	case StatusReported:
		return "Reported (the spec in use was not changed)"
	case StatusDiscarded:
		return "Discarded"
	default:
		return "Applied"
	}
}

// ChangeBody is a change report as markdown. Every value read from the spec
// or a caller is a code span, so none of it is read as markup.
func ChangeBody(c Change) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n%s\n", c.Report.Counts(), statusLabel(c.Status))
	section := func(name string, ops []Op) {
		if len(ops) == 0 {
			return
		}
		fmt.Fprintf(&b, "\n**%s**\n\n", name)
		for i, op := range ops {
			if i == maxNotified {
				fmt.Fprintf(&b, "- and %d more\n", len(ops)-maxNotified)
				break
			}
			fmt.Fprintf(&b, "- %s\n", opLine(op))
		}
	}
	section("Removed", c.Report.Removed)
	changed := make([]Op, 0, len(c.Report.Changed))
	for _, ch := range c.Report.Changed {
		op := ch.Op
		op.Summary = strings.Join(ch.Parts, ", ")
		changed = append(changed, op)
	}
	section("Changed", changed)
	section("Deprecated", c.Report.Deprecated)
	section("Added", c.Report.Added)
	return b.String()
}

// opLine is one operation, what changed in it, and what uses it.
func opLine(op Op) string {
	parts := []string{code(op.ID)}
	if addr := op.Method + space + op.Path; op.Method != "" && op.ID != addr {
		parts = append(parts, space, code(addr))
	}
	if op.Summary != "" {
		parts = append(parts, ": ", code(op.Summary))
	}
	if names := callerNames(op.UsedBy); len(names) > 0 {
		parts = append(parts, "; used by ", strings.Join(names, ", "))
	}
	return strings.Join(parts, "")
}

// callerNames is each caller once, as "kind `name`".
func callerNames(callers []Caller) []string {
	names := make([]string, 0, len(callers))
	seen := map[string]bool{}
	for _, c := range callers {
		key := c.Kind + space + c.Name
		if !seen[key] {
			seen[key] = true
			names = append(names, c.Kind+space+code(c.Name))
		}
	}
	return names
}

// code is s as a markdown code span with no backtick of its own.
func code(s string) string {
	return "`" + strings.ReplaceAll(s, "`", "'") + "`"
}
