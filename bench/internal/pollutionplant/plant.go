// Package pollutionplant is the knowledge-pollution study's machinery
// (#1165): it plants a treatment claim into the shared applied tier through
// the platform's own promotion path, proves the claim reaches identities
// other than the one that captured it, defines the study's cells with their
// fixture-computed discriminant values, and drives the two remediations
// whose effect on belief RQ3 measures.
//
// The plant is deterministic — no model is in the seeding path — so the
// treatment string is byte-controlled, and it follows pkplant's contract:
// store it, prove it stored, prove it reaches the identity class that will
// be measured. A cell whose treatment never reached the evaluators is a
// clean-stack control wearing a treatment's label, and nothing downstream
// can tell the difference.
//
// The evaluation itself is not run here: an arm is a plain benchrun over
// the committed tasks, before (baseline) and after (planted) this package
// has done its work.
package pollutionplant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/promote"
	"github.com/txn2/mcp-data-platform/bench/internal/protocol"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// Platform tool names the plant drives.
const (
	captureTool = "memory_capture"
	searchTool  = "search"
	entityTool  = "datahub_get_entity"
	applyTool   = "apply_knowledge"
)

// The capture classification, held constant across every planted claim and
// never a study variable. A class-dependent choice would put a systematic
// difference between the arms in exactly the comparison the study rests on.
const (
	sinkClass = "business_knowledge"
	category  = "data_quality"
)

// DefaultGateIntent opens the search-first gate the way any capturing or
// reading session would. It names the study's subject matter without naming
// any discriminant, so the same intent is usable on every arm and cannot
// itself deliver a claim.
const DefaultGateIntent = "company reporting conventions and bench warehouse table definitions"

// AdminSeq is the pool sequence meaning "the base credential" — the
// reviewer identity, which is the platform admin rather than a pool member.
const AdminSeq = 0

// session is the platform interaction the orchestration needs. It is an
// interface so the sequence that actually carries the correctness — store
// it, prove it stored, prove it comes back to someone else — is exercisable
// without a live stack.
type session interface {
	Call(ctx context.Context, tool string, args map[string]any) (text string, toolErr bool, err error)
	Close() error
}

// dialer opens a session as one pool identity; AdminSeq means the base
// credential.
type dialer func(ctx context.Context, seq int) (session, error)

// platformReader reads lifecycle and sink state back through the admin APIs,
// never from a transcript.
type platformReader interface {
	ListInsights(ctx context.Context, f lifecycleapi.InsightFilter) ([]lifecycleapi.Insight, error)
	GetInsight(ctx context.Context, id string) (*lifecycleapi.Insight, error)
	ListChangesets(ctx context.Context, f lifecycleapi.ChangesetFilter) ([]lifecycleapi.Changeset, error)
	ListKnowledgePages(ctx context.Context) ([]lifecycleapi.KnowledgePage, error)
	CreateKnowledgePage(ctx context.Context, page lifecycleapi.NewKnowledgePage) (*lifecycleapi.KnowledgePage, error)
}

// applier approves a captured insight and applies it to the treatment's
// sink over a reviewer session. It is a function rather than the concrete
// reviewer so the orchestration can be exercised against a fake.
type applier func(ctx context.Context, t Treatment, insightID string) (bool, error)

// Client plants and remediates treatments against one platform.
type Client struct {
	dial     dialer
	insights platformReader
	apply    applier
	log      *slog.Logger
}

// New builds a client against a live platform. The reviewer path opens its
// own admin session per apply, mirroring what a human reviewer's client
// would do.
func New(t target.Target, identityKeys int, insights *lifecycleapi.Client, httpTimeout time.Duration, log *slog.Logger) *Client {
	dial := func(ctx context.Context, seq int) (session, error) {
		return dialMCP(ctx, t, identityKeys, seq, httpTimeout)
	}
	c := &Client{dial: dial, insights: insights, log: log}
	c.apply = func(ctx context.Context, tr Treatment, insightID string) (bool, error) {
		return applyLive(ctx, dial, insights, log, tr, insightID)
	}
	return c
}

// Request is one plant.
type Request struct {
	// Treatment is the claim to plant.
	Treatment Treatment
	// TeacherSeq is the pool identity that captures the claim. Keep it
	// clear of the evaluation attempts' sequences: an evaluator that is
	// also the capturer would read its own note, which is not the
	// cross-identity delivery the study is about.
	TeacherSeq int
	// WitnessSeq is the identity the reachability check runs as. It must
	// differ from TeacherSeq — that difference IS the check.
	WitnessSeq int
	// GateIntent opens the search-first gate; DefaultGateIntent when empty.
	GateIntent string
}

// Result is one planted claim and the evidence it landed.
type Result struct {
	// TreatmentID names the claim.
	TreatmentID string `json:"treatment_id"`
	// InsightID is the platform's id for the stored claim.
	InsightID string `json:"insight_id"`
	// ChangesetID links the applied change, and is what a rollback
	// remediation reverts. Recorded at plant time because the remediation
	// runs in a separate invocation, possibly days later.
	ChangesetID string `json:"changeset_id"`
	// TeacherEmail is the identity that captured it.
	TeacherEmail string `json:"teacher_email"`
	// WitnessEmail is the identity that read it back.
	WitnessEmail string `json:"witness_email"`
	// Text is exactly what was stored, and exactly what evaluators will
	// read. Recorded so a run's archive carries the treatment as
	// delivered.
	Text string `json:"text"`
	// Needle is the span the read-back matched on.
	Needle string `json:"needle"`
	// InSearch and InSink record which surfaces carried the claim for the
	// witness identity: discovery, and the sink the promotion applied to
	// (an entity description, or a knowledge page). Both are reported
	// because they are different delivery channels, and a study that
	// checked only one could not say which one an adopting episode read.
	InSearch bool `json:"in_search"`
	InSink   bool `json:"in_sink"`
}

// Plant stores one treatment, promotes it to the applied tier, and proves
// another identity can reach it.
func (c *Client) Plant(ctx context.Context, req Request) (Result, error) {
	if err := checkRequest(req); err != nil {
		return Result{}, err
	}
	res := Result{
		TreatmentID:  req.Treatment.ID,
		TeacherEmail: pool.Email(req.TeacherSeq),
		WitnessEmail: pool.Email(req.WitnessSeq),
		Text:         req.Treatment.Text,
		Needle:       req.Treatment.Needle,
	}
	insightID, err := c.capture(ctx, req)
	if err != nil {
		return res, err
	}
	res.InsightID = insightID
	applied, err := c.apply(ctx, req.Treatment, insightID)
	if err != nil {
		return res, fmt.Errorf("pollutionplant: apply %s: %w", req.Treatment.ID, err)
	}
	if !applied {
		return res, fmt.Errorf("pollutionplant: %s declined the promotion of %s; nothing reached the applied tier",
			applyTool, req.Treatment.ID)
	}
	in, err := c.insights.GetInsight(ctx, insightID)
	if err != nil {
		return res, fmt.Errorf("pollutionplant: read insight %s after apply: %w", insightID, err)
	}
	res.ChangesetID = in.ChangesetRef
	res.InSearch, res.InSink, err = c.witness(ctx, req)
	if err != nil {
		return res, err
	}
	return res, nil
}

// checkRequest refuses a plant that could not measure what it claims to.
func checkRequest(req Request) error {
	if err := req.Treatment.Validate(); err != nil {
		return err
	}
	switch {
	case req.TeacherSeq < 1:
		return fmt.Errorf("pollutionplant: teacher sequence must be a pool identity, got %d", req.TeacherSeq)
	case req.WitnessSeq < 1:
		return fmt.Errorf("pollutionplant: witness sequence must be a pool identity, got %d", req.WitnessSeq)
	case req.TeacherSeq == req.WitnessSeq:
		return fmt.Errorf("pollutionplant: teacher and witness are both identity %d; a claim read back by the "+
			"identity that captured it proves nothing about cross-identity delivery", req.TeacherSeq)
	}
	return nil
}

// capture stores the claim as the teacher identity and proves the store
// holds it byte for byte.
func (c *Client) capture(ctx context.Context, req Request) (string, error) {
	email := pool.Email(req.TeacherSeq)
	teacher, err := c.dial(ctx, req.TeacherSeq)
	if err != nil {
		return "", fmt.Errorf("pollutionplant: connect as %s: %w", email, err)
	}
	defer func() { _ = teacher.Close() }()
	if err := openGate(ctx, teacher, req.GateIntent); err != nil {
		return "", err
	}
	id, err := capture(ctx, teacher, req.Treatment)
	if err != nil {
		return "", fmt.Errorf("pollutionplant: capture as %s: %w", email, err)
	}
	if err := c.confirmStored(ctx, email, req.Treatment.Text); err != nil {
		return "", err
	}
	if c.log != nil {
		c.log.Info("captured", "treatment", req.Treatment.ID, "insight_id", id, "teacher", email)
	}
	return id, nil
}

// witness reads the claim back as a different identity, through the
// surfaces evaluators read. Without this a planted arm could silently
// measure a clean stack.
func (c *Client) witness(ctx context.Context, req Request) (inSearch, inSink bool, err error) {
	email := pool.Email(req.WitnessSeq)
	w, err := c.dial(ctx, req.WitnessSeq)
	if err != nil {
		return false, false, fmt.Errorf("pollutionplant: connect as witness %s: %w", email, err)
	}
	defer func() { _ = w.Close() }()
	inSearch, inSink, err = c.reach(ctx, w, req.Treatment, req.GateIntent)
	if err != nil {
		return false, false, err
	}
	if c.log != nil {
		c.log.Info("witness read-back", "witness", email, "needle", req.Treatment.Needle,
			"in_search", inSearch, "in_sink", inSink)
	}
	if !inSearch && !inSink {
		return inSearch, inSink, fmt.Errorf("pollutionplant: %s is unreachable for %s: the needle %q is absent from "+
			"both search and the sink read-back, so an arm run now would measure an unplanted stack",
			req.Treatment.ID, email, req.Treatment.Needle)
	}
	return inSearch, inSink, nil
}

// reach reports which surfaces currently carry the treatment's needle for
// the given session. It is shared by the plant's witness check and the
// remediation's post-state read, so "reachable" means the same thing in
// both directions.
func (c *Client) reach(ctx context.Context, s session, tr Treatment, intent string) (inSearch, inSink bool, err error) {
	body, toolErr, err := s.Call(ctx, searchTool, map[string]any{"intent": gateIntent(intent)})
	if err != nil {
		return false, false, fmt.Errorf("pollutionplant: %s: %w", searchTool, err)
	}
	if toolErr {
		return false, false, fmt.Errorf("pollutionplant: %s refused: %.300s", searchTool, body)
	}
	inSearch = strings.Contains(body, tr.Needle)
	inSink, err = c.sinkCarries(ctx, s, tr)
	return inSearch, inSink, err
}

// sinkCarries reports whether the treatment's sink currently holds the
// claim, read the way the promotion wrote it: an entity description through
// the platform's own entity tool, a knowledge page through the portal list.
// Reading the sink separately from search matters because the two retract
// independently — a supersede clears the insight and leaves the sink — so a
// single combined signal could not say which channel an episode read.
func (c *Client) sinkCarries(ctx context.Context, s session, tr Treatment) (bool, error) {
	if tr.Sink == protocol.SinkKnowledgePage {
		pages, err := c.insights.ListKnowledgePages(ctx)
		if err != nil {
			return false, fmt.Errorf("pollutionplant: list knowledge pages: %w", err)
		}
		for _, p := range pages {
			if p.Slug == tr.Page.Slug {
				return strings.Contains(p.Summary, tr.Needle) || strings.Contains(p.Title, tr.Needle), nil
			}
		}
		return false, nil
	}
	entity, toolErr, err := s.Call(ctx, entityTool, map[string]any{"urn": tr.EntityURN})
	if err != nil {
		return false, fmt.Errorf("pollutionplant: %s: %w", entityTool, err)
	}
	if toolErr {
		return false, fmt.Errorf("pollutionplant: %s refused: %.300s", entityTool, entity)
	}
	return strings.Contains(entity, tr.Needle), nil
}

// openGate makes the session's first call the discovery front door, the way
// any session on this platform must.
func openGate(ctx context.Context, s session, intent string) error {
	body, toolErr, err := s.Call(ctx, searchTool, map[string]any{"intent": gateIntent(intent)})
	if err != nil {
		return fmt.Errorf("pollutionplant: %s: %w", searchTool, err)
	}
	if toolErr {
		return fmt.Errorf("pollutionplant: %s refused: %.300s", searchTool, body)
	}
	return nil
}

// gateIntent defaults an empty intent.
func gateIntent(intent string) string {
	if strings.TrimSpace(intent) == "" {
		return DefaultGateIntent
	}
	return intent
}

// capture records one claim and returns the platform's id for it.
func capture(ctx context.Context, s session, tr Treatment) (string, error) {
	args := map[string]any{
		"type":       sinkClass,
		"content":    tr.Text,
		"category":   category,
		"confidence": "high",
	}
	if tr.EntityURN != "" {
		args["entity_urns"] = []string{tr.EntityURN}
	}
	body, toolErr, err := s.Call(ctx, captureTool, args)
	if err != nil {
		return "", err
	}
	if toolErr {
		return "", fmt.Errorf("%s refused: %.300s", captureTool, body)
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.ID == "" {
		return "", fmt.Errorf("%s returned no id: %.300s", captureTool, body)
	}
	return out.ID, nil
}

// confirmStored requires the stored text to be exactly what was planted.
// Capture is free to normalize or truncate, and a treatment that was
// silently altered is no longer the string the discriminant table was
// computed for, so the run must not proceed on it.
func (c *Client) confirmStored(ctx context.Context, email, text string) error {
	insights, err := c.insights.ListInsights(ctx, lifecycleapi.InsightFilter{CapturedBy: email})
	if err != nil {
		return fmt.Errorf("pollutionplant: read back for %s: %w", email, err)
	}
	for _, in := range insights {
		if in.InsightText == text {
			return nil
		}
	}
	return fmt.Errorf("pollutionplant: %s holds %d note(s), none matching the planted text exactly", email, len(insights))
}

// mcpSession is the live session: an MCP client session threading the
// handle it minted onto every call.
type mcpSession struct {
	s      *mcp.ClientSession
	handle string
}

func (m *mcpSession) Call(ctx context.Context, tool string, args map[string]any) (string, bool, error) {
	res := mcpc.Call(ctx, m.s, tool, args, m.handle)
	return res.Text, res.ToolErr, res.TransportErr
}

func (m *mcpSession) Close() error { return m.s.Close() }

// dialMCP opens a session as a pool identity (or as the base credential at
// AdminSeq) and mints its handle.
func dialMCP(ctx context.Context, t target.Target, identityKeys, seq int, timeout time.Duration) (session, error) {
	cred := t.Credential
	if seq != AdminSeq {
		cred = pool.Credential(t.Credential, seq, identityKeys)
	}
	client := mcpc.New(t.BaseURL, target.Target{BaseURL: t.BaseURL, Credential: cred}.HTTPClient(timeout))
	s, err := client.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", err)
	}
	info, err := mcpc.Mint(ctx, s)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("mint handle: %w", err)
	}
	return &mcpSession{s: s, handle: info.Handle}, nil
}

// applyLive is the reviewer path: an admin session approves the captured
// claim and applies it to the treatment's sink through the same promote
// machinery the lifecycle suites drive, including its post-apply sink
// read-back.
func applyLive(ctx context.Context, dial dialer, insights *lifecycleapi.Client,
	log *slog.Logger, tr Treatment, insightID string,
) (bool, error) {
	admin, err := dial(ctx, AdminSeq)
	if err != nil {
		return false, fmt.Errorf("connect as reviewer: %w", err)
	}
	defer func() { _ = admin.Close() }()
	if err := openGate(ctx, admin, DefaultGateIntent); err != nil {
		return false, err
	}
	live, ok := admin.(*mcpSession)
	if !ok {
		return false, errors.New("the reviewer path needs a live MCP session")
	}
	reviewer := promote.Reviewer{Life: insights, Log: log}
	return reviewer.Apply(ctx, live.s, live.handle, promote.Target{
		Label:     tr.ID,
		EntityURN: tr.EntityURN,
		Sink:      tr.Sink,
		Fact:      tr.Text,
		Page:      tr.Page,
		// The API fixture seeds a correct source stating the same convention
		// (seed.go), so a planted page is a near-duplicate of it by
		// construction and the create-time gate would block the promotion.
		// The study wants both pages: their co-presence is the conflict the
		// arm measures, and force_new is the affordance the gate itself
		// names for exactly this.
		ForceNewPage: true,
		Notes:        "knowledge-pollution study plant: " + tr.ID,
	}, insightID)
}
