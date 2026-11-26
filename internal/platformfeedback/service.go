package platformfeedback

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/internal/opranking"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/portal/assetsimilar"
	"github.com/txn2/mcp-data-platform/internal/secretscan"
	"github.com/txn2/mcp-data-platform/pkg/notification"
)

// keyPrefix is the channel document key a post's sends carry, which is also
// how its deliveries are read back.
const keyPrefix = "platform_feedback:"

// day is the persona limit's window.
const day = 24 * time.Hour

// maxTitleRunes is how much of what it was trying to do a send's title shows.
const maxTitleRunes = 120

// Refusal reasons recorded, never the content.
const (
	reasonPersona = "persona"
	reasonInvalid = "invalid"
	reasonSecret  = "secret"
	reasonLimit   = "limit"
)

// ChannelSender queues a document for one channel; notification.Enqueuer is
// one.
type ChannelSender interface {
	NotifyChannel(ctx context.Context, s notification.ChannelSend) (notification.ChannelResult, error)
}

// Deps is what the service reads.
type Deps struct {
	// DB holds the posts and the notification queue rows their sends wrote.
	DB *sql.DB
	// Section holds the settings; nil is "off".
	Section *settingsjson.Section[Settings]
	// Detector checks every text field for a credential before anything is
	// written.
	Detector secretscan.Detector
	// Embed turns a post's words into a vector for folding a repeat; nil
	// folds only an exact repeat.
	Embed func(ctx context.Context, text string) ([]float32, error)
	// EmbedTimeout bounds Embed; zero is DefaultEmbedTimeout. A post whose
	// embedding does not come back in time is stored and sent unfolded, but
	// for a repeat of its exact words (#2829).
	EmbedTimeout time.Duration
	// Channels resolves a channel name; Sender queues a send to it.
	Channels notification.ChannelStore
	Sender   ChannelSender
	// ServiceAccount reports a persona whose calls are machinery: it may not
	// post when the settings name no personas.
	ServiceAccount func(persona string) bool
	// KnownPersona checks a persona a settings save names.
	KnownPersona func(persona string) bool
	// PortalURL is the portal's public address, for the link a send carries.
	PortalURL string
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// DefaultEmbedTimeout is how long a post waits for its embedding (#2829). The
// embedding only folds a near-duplicate onto an open post, so a busy embedding
// service, as right after an upgrade re-embeds the platform's knowledge, costs
// a missed fold and never the report.
const DefaultEmbedTimeout = 5 * time.Second

// Service is platform feedback.
type Service struct {
	d     Deps
	store pgStore
}

// New builds the service.
func New(d Deps) *Service {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.EmbedTimeout <= 0 {
		d.EmbedTimeout = DefaultEmbedTimeout
	}
	return &Service{d: d, store: pgStore{db: d.DB}}
}

// Settings is the settings in force, with who saved them.
func (s *Service) Settings(ctx context.Context) settingsjson.Stored[Settings] {
	if s == nil || s.d.Section == nil {
		return settingsjson.Stored[Settings]{Value: Settings{}.withDefaults()}
	}
	got, err := s.d.Section.Get(ctx)
	if err != nil {
		return settingsjson.Stored[Settings]{Value: Settings{}.withDefaults()}
	}
	got.Value = got.Value.withDefaults()
	return got
}

// SaveSettings validates and stores the settings.
func (s *Service) SaveSettings(ctx context.Context, in Settings, author string) (settingsjson.Stored[Settings], error) {
	knownChannel := func(name string) bool {
		if s.d.Channels == nil {
			return false
		}
		_, err := s.d.Channels.Get(ctx, name)
		return err == nil
	}
	if err := in.validate(s.d.KnownPersona, knownChannel); err != nil {
		return settingsjson.Stored[Settings]{}, err
	}
	if s.d.Section == nil {
		return settingsjson.Stored[Settings]{}, errors.New("platform feedback settings are not stored on this deployment")
	}
	if err := s.d.Section.Set(ctx, in.withDefaults(), author); err != nil {
		return settingsjson.Stored[Settings]{}, err //nolint:wrapcheck // names the section
	}
	return s.Settings(ctx), nil
}

// Submit checks, stores and sends one post.
func (s *Service) Submit(ctx context.Context, c Caller, p Post) (Result, error) {
	set := s.Settings(ctx).Value
	p, err := s.admit(ctx, set, c, p)
	if err != nil {
		return Result{}, err
	}
	return s.post(ctx, set, c, p)
}

// admit refuses a post the settings, its fields, the secret check or the
// limits do not let through, recording each refusal's reason; a failed
// limit count is an error, not a refusal, and is not recorded as one.
func (s *Service) admit(ctx context.Context, set Settings, c Caller, p Post) (Post, error) {
	if !set.Enabled {
		return p, refused(CodeOff, "platform feedback is off on this deployment; an administrator turns it on in Admin > Settings > Platform feedback")
	}
	if !s.mayPost(set, c.Persona) {
		s.recordRefusal(ctx, reasonPersona, c.Persona)
		return p, refused(CodePersona, "persona %q may not post platform feedback", c.Persona)
	}
	p = p.normalized()
	if err := p.check(); err != nil {
		s.recordRefusal(ctx, reasonInvalid, c.Persona)
		return p, err
	}
	if err := s.scan(ctx, p); err != nil {
		s.recordRefusal(ctx, reasonSecret, c.Persona)
		return p, err
	}
	err := s.withinLimits(ctx, set, c)
	if _, isRefusal := errors.AsType[*RefusedError](err); isRefusal {
		s.recordRefusal(ctx, reasonLimit, c.Persona)
	}
	return p, err
}

// post stores an admitted post, or counts it on the open post it repeats,
// and sends a new one to the channels.
func (s *Service) post(ctx context.Context, set Settings, c Caller, p Post) (Result, error) {
	report := Report{Persona: c.Persona, UserID: c.UserID, SessionID: c.SessionID, Calls: p.Calls}
	emb := s.embed(ctx, p.text())
	if id, ok := s.repeatOf(ctx, p, emb); ok {
		count, err := s.store.addReport(ctx, id, report)
		if err != nil {
			return Result{}, err
		}
		return Result{
			ID: id, ReportCount: count, Folded: true,
			Message: "This repeats an open post, so it was counted on it and not sent again.",
		}, nil
	}
	id, err := newID()
	if err != nil {
		return Result{}, err
	}
	if err := s.store.create(ctx, id, p, emb, report); err != nil {
		return Result{}, err
	}
	sent := s.send(ctx, sending{channels: set.Channels, key: keyPrefix + id, id: id, post: p, caller: c})
	return Result{
		ID: id, ReportCount: 1, Channels: sent,
		Message: fmt.Sprintf("Posted to the platform's operator; queued for %d channel(s).", sent),
	}, nil
}

// mayPost reports a persona the settings let post.
func (s *Service) mayPost(set Settings, persona string) bool {
	if len(set.Personas) > 0 {
		return slices.Contains(set.Personas, persona)
	}
	return s.d.ServiceAccount == nil || !s.d.ServiceAccount(persona)
}

// scan refuses a post with a credential in any field, naming the field and
// the kind of match and never the value.
func (s *Service) scan(ctx context.Context, p Post) error {
	findings, err := s.d.Detector.Scan(ctx, []secretscan.Field{
		{Name: fieldTryingTo, Value: p.TryingTo},
		{Name: fieldBlockedBy, Value: p.BlockedBy},
		{Name: fieldTool, Value: p.Tool},
		{Name: fieldSuggestion, Value: p.Suggestion},
		{Name: fieldCalls, Value: strings.Join(p.Calls, " ")},
	})
	if err != nil {
		slog.WarnContext(ctx, "platform feedback: secret check failed; refusing the post", "error", logsan.SanitizeForLog(err.Error()))
		return refused(CodeSecret, "the post could not be checked for credentials, so it was not posted; try again shortly")
	}
	if len(findings) == 0 {
		return nil
	}
	f := findings[0]
	return refused(CodeSecret, "%s holds %s, and a post never carries a credential; remove it and post again", f.Field, f.Kind)
}

// withinLimits refuses a post past the session or the persona limit.
func (s *Service) withinLimits(ctx context.Context, set Settings, c Caller) error {
	bySession, byPersona, err := s.store.reportsSince(ctx, c.SessionID, c.Persona, s.d.Now().Add(-day))
	if err != nil {
		return err
	}
	if c.SessionID != "" && bySession >= set.SessionLimit {
		return refused(CodeLimit, "this session has posted %d times, the most one session may (session_limit %d)", bySession, set.SessionLimit)
	}
	if byPersona >= set.PersonaDailyLimit {
		return refused(CodeLimit, "persona %q has posted %d times today, the most it may (persona_daily_limit %d)", c.Persona, byPersona, set.PersonaDailyLimit)
	}
	return nil
}

// embed is the post's vector, nil when there is no embedder, it failed, or it
// did not answer within EmbedTimeout (#2829), which is logged: the post is
// then folded only onto an open post with its exact words.
func (s *Service) embed(ctx context.Context, text string) []float32 {
	if s.d.Embed == nil {
		return nil
	}
	ectx, cancel := context.WithTimeout(ctx, s.d.EmbedTimeout)
	defer cancel()
	v, err := s.d.Embed(ectx, text)
	if err != nil {
		if ctx.Err() == nil && errors.Is(ectx.Err(), context.DeadlineExceeded) {
			slog.WarnContext(ctx, "platform feedback: the embedding did not answer in time; the post is folded only by its words",
				"timeout", s.d.EmbedTimeout.String())
		}
		return nil
	}
	return v
}

// repeatOf is the open post this one repeats: the closest by embedding at the
// near-duplicate threshold the asset similarity check uses, or one with the
// same words.
func (s *Service) repeatOf(ctx context.Context, p Post, emb []float32) (string, bool) {
	open, err := s.store.open(ctx)
	if err != nil {
		return "", false
	}
	want := p.text()
	var (
		best      string
		bestScore float64
	)
	for _, o := range open {
		if sameWords(o.text, want) {
			return o.id, true
		}
		if len(emb) > 0 && len(o.embedding) == len(emb) {
			if score := opranking.Cosine(emb, o.embedding); score >= assetsimilar.NearDuplicate && score > bestScore {
				best, bestScore = o.id, score
			}
		}
	}
	return best, best != ""
}

// sameWords reports two texts holding the same words in the same order,
// whatever their case, punctuation or spacing: a repeat an agent wrote with a
// period added is the same report.
func sameWords(a, b string) bool {
	return slices.Equal(words(a), words(b))
}

// words is a text's words, lower case, letters and digits only.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
}

// sending is one post sent to channels under a notification key.
type sending struct {
	channels []string
	key, id  string
	post     Post
	caller   Caller
}

// send queues the post for each channel, and is how many it reached.
func (s *Service) send(ctx context.Context, sn sending) int {
	if s.d.Channels == nil || s.d.Sender == nil {
		return 0
	}
	doc := s.document(sn.id, sn.post, sn.caller)
	doc.Key = sn.key
	sent := 0
	for _, name := range sn.channels {
		ch, err := s.d.Channels.Get(ctx, name)
		if err != nil {
			slog.WarnContext(ctx, "platform feedback: channel unavailable", "channel", logsan.SanitizeForLog(name))
			continue
		}
		res, err := s.d.Sender.NotifyChannel(ctx, notification.ChannelSend{Channel: *ch, Document: doc, Platform: true})
		if err != nil {
			slog.WarnContext(ctx, "platform feedback: queueing a send failed", "channel", logsan.SanitizeForLog(name),
				"error", logsan.SanitizeForLog(err.Error()))
			continue
		}
		if res.Queued > 0 {
			sent++
		}
	}
	return sent
}

// document is what a channel is sent: the fields as markdown, and as data a
// JSON webhook delivers verbatim.
func (s *Service) document(id string, p Post, c Caller) notification.Document {
	var b strings.Builder
	fmt.Fprintf(&b, "**Trying to:** %s\n\n**Blocked by:** %s\n", p.TryingTo, p.BlockedBy)
	if p.Tool != "" {
		fmt.Fprintf(&b, "\n**Tool:** %s\n", p.Tool)
	}
	if p.Suggestion != "" {
		fmt.Fprintf(&b, "\n**Suggestion:** %s\n", p.Suggestion)
	}
	if len(p.Calls) > 0 {
		fmt.Fprintf(&b, "\n**Calls:** %s\n", strings.Join(p.Calls, ", "))
	}
	data, _ := json.Marshal(map[string]any{
		"id": id, fieldTryingTo: p.TryingTo, fieldBlockedBy: p.BlockedBy, fieldTool: p.Tool,
		fieldSuggestion: p.Suggestion, fieldCalls: p.Calls, "persona": c.Persona,
	})
	title := p.TryingTo
	if r := []rune(title); len(r) > maxTitleRunes {
		title = string(r[:maxTitleRunes]) + "..."
	}
	link := ""
	if s.d.PortalURL != "" {
		link = strings.TrimRight(s.d.PortalURL, "/") + "/portal/admin/platform-feedback/" + id
	}
	return notification.Document{
		Title: "Platform feedback: " + title, Body: b.String(), Link: link, Key: keyPrefix + id,
		Type: notification.DocumentPlatformFeedback, Data: data,
	}
}

// recordRefusal keeps why a post was refused, never what it said.
func (s *Service) recordRefusal(ctx context.Context, reason, persona string) {
	if err := s.store.refusal(ctx, reason, persona); err != nil {
		slog.WarnContext(ctx, "platform feedback: recording a refusal failed", "error", logsan.SanitizeForLog(err.Error()))
	}
}

// newID is a post's id.
func newID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating a platform feedback id: %w", err)
	}
	return "pfb_" + hex.EncodeToString(b), nil
}

// List is the posts a filter selects, with the page's counts.
func (s *Service) List(ctx context.Context, f ListFilter) (ListResponse, error) {
	items, total, err := s.store.list(ctx, f)
	if err != nil {
		return ListResponse{}, err
	}
	stats, err := s.store.stats(ctx, s.d.Now().Add(-7*day))
	if err != nil {
		return ListResponse{}, err
	}
	return ListResponse{Posts: items, Total: total, Stats: stats}, nil
}

// Get is one post in full.
func (s *Service) Get(ctx context.Context, id string) (Detail, error) {
	it, err := s.store.get(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	reports, err := s.store.reports(ctx, id)
	if err != nil {
		return Detail{}, err
	}
	deliveries, err := s.store.deliveries(ctx, keyPrefix+id)
	if err != nil {
		return Detail{}, err
	}
	return Detail{Item: it, Reports: reports, Deliveries: deliveries}, nil
}

// SetStatus moves a post to a status.
func (s *Service) SetStatus(ctx context.Context, id, status, by string) error {
	if !validStatus(status) {
		return &InvalidError{Msg: "status: one of open, planned, done, declined"}
	}
	return s.store.setStatus(ctx, id, status, by)
}

// Resend queues a post again for one channel the settings name, as a
// delivery that failed is retried by hand.
func (s *Service) Resend(ctx context.Context, id, channel string) error {
	set := s.Settings(ctx).Value
	if !slices.Contains(set.Channels, channel) {
		return &InvalidError{Msg: fmt.Sprintf("channel %q is not one platform feedback is sent to", channel)}
	}
	it, err := s.store.get(ctx, id)
	if err != nil {
		return err
	}
	// A resend carries a key of its own, so the channel's repeat window does
	// not absorb it as the same send again.
	key := fmt.Sprintf("%s%s:resend:%d", keyPrefix, id, s.d.Now().UnixNano())
	post := Post{TryingTo: it.TryingTo, BlockedBy: it.BlockedBy, Tool: it.Tool, Suggestion: it.Suggestion}
	if s.send(ctx, sending{channels: []string{channel}, key: key, id: id, post: post}) == 0 {
		return fmt.Errorf("channel %q did not take the send", channel)
	}
	return nil
}
