// Package pkplant plants a frozen seed into the platform as the identity
// that will later be asked about it (#1054, work item 4).
//
// It plants through the platform's own capture tool over MCP, because
// there is no admin endpoint that creates a note and because that is the
// path a real prior session would have taken. It plants as the querying
// identity because insight search is scoped to the caller: a note captured
// by anyone else is invisible, which also makes seeding-as-self the
// faithful model of the case under study, an agent holding a belief from
// its own earlier session.
//
// The plant is not complete until the note has been shown to come back.
// A cell whose belief never reaches the agent is a no-knowledge control
// wearing a knowledge cell's label, and nothing downstream can tell the
// difference.
package pkplant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/txn2/mcp-data-platform/bench/internal/lifecycleapi"
	"github.com/txn2/mcp-data-platform/bench/internal/mcpc"
	"github.com/txn2/mcp-data-platform/bench/internal/pkseed"
	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/target"
)

// Tool names on the platform surface.
const (
	captureTool = "memory_capture"
	searchTool  = "search"
)

// sinkClass and category are held constant across every planted seed.
// Neither is a study variable, and a class-dependent choice would put a
// systematic difference between the perishable beliefs and the durable and
// eternal controls, in exactly the comparison the discriminant clause of
// H3 rests on.
const (
	sinkClass = "business_knowledge"
	category  = "data_quality"
)

// session is the platform interaction a plant needs. It is an interface so
// the orchestration below — store it, prove it was stored, prove it comes
// back — is exercisable without a live stack, which is where the
// correctness of a plant actually lives.
type session interface {
	Call(ctx context.Context, tool string, args map[string]any) (text string, toolErr bool, err error)
	Close() error
}

// dialer opens a session as one pool identity.
type dialer func(ctx context.Context, seq int) (session, error)

// reader reads a note back by the identity that captured it.
type reader interface {
	ListInsights(ctx context.Context, f lifecycleapi.InsightFilter) ([]lifecycleapi.Insight, error)
}

// Client plants seeds against one platform.
type Client struct {
	dial     dialer
	insights reader
}

// New builds a planter against a live platform. insights is used to read a
// planted note back by the identity that captured it.
func New(t target.Target, identityKeys int, insights *lifecycleapi.Client, timeout time.Duration) *Client {
	return &Client{
		dial: func(ctx context.Context, seq int) (session, error) {
			return dialMCP(ctx, t, identityKeys, seq, timeout)
		},
		insights: insights,
	}
}

// Request is one plant.
type Request struct {
	// Seed is the frozen belief to plant.
	Seed pkseed.Seed
	// Metadata is the delivery arm; the zero value is the bare arm.
	Metadata pkseed.Metadata
	// Seq is the pool identity that captures the note and will later be
	// asked the question.
	Seq int
	// Probe is the question the cell will put to the agent. When set, the
	// plant is verified by searching for it as the planting identity and
	// requiring the note to come back, so a cell cannot silently run as a
	// no-knowledge control. Empty skips the check, which is only
	// appropriate when the caller verifies reachability another way.
	Probe string
}

// Result is one planted note.
type Result struct {
	// InsightID is the platform's id for the stored note.
	InsightID string `json:"insight_id"`
	// Email is the identity that holds it.
	Email string `json:"email"`
	// Text is exactly what was stored, and exactly what the agent will
	// read. Recorded so a run's archive contains the treatment as
	// delivered rather than a recipe for reconstructing it.
	Text string `json:"text"`
	// Probed is true when reachability was verified by search.
	Probed bool `json:"probed"`
}

// Plant stores one seed and verifies it comes back.
func (c *Client) Plant(ctx context.Context, req Request) (Result, error) {
	text := pkseed.Delivered(req.Seed, req.Metadata)
	if err := checkDeliverable(req, text); err != nil {
		return Result{}, err
	}
	email := pool.Email(req.Seq)
	sess, err := c.dial(ctx, req.Seq)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = sess.Close() }()

	id, err := capture(ctx, sess, text)
	if err != nil {
		return Result{}, fmt.Errorf("pkplant: capture as %s: %w", email, err)
	}
	if err := c.confirmStored(ctx, email, text); err != nil {
		return Result{}, err
	}
	res := Result{InsightID: id, Email: email, Text: text}
	if req.Probe == "" {
		return res, nil
	}
	if err := probe(ctx, sess, req.Probe, req.Seed.Asserts); err != nil {
		return res, fmt.Errorf("pkplant: %s planted but not reachable: %w", email, err)
	}
	res.Probed = true
	return res, nil
}

// checkDeliverable refuses a plant whose delivered text would violate the
// audited invariants. The audit gates the build; this gates the run, so a
// treatment string cannot reach an agent by a path the build never saw.
func checkDeliverable(req Request, text string) error {
	if req.Seq < 1 {
		return fmt.Errorf("pkplant: identity sequence must be positive, got %d", req.Seq)
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("pkplant: seed delivers no text")
	}
	if err := pkseed.ValidateMetadata(req.Metadata); err != nil {
		return err
	}
	if found := pkseed.AuditDelivered(text); len(found) > 0 {
		return fmt.Errorf("pkplant: seed %s violates the delivery invariants: %s",
			req.Seed.ID, strings.Join(found, ", "))
	}
	return nil
}

// mcpSession is the live implementation: an MCP session that threads the
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

// dialMCP opens a session as a pool identity and mints its handle.
func dialMCP(ctx context.Context, t target.Target, identityKeys, seq int, timeout time.Duration) (session, error) {
	cred := pool.Credential(t.Credential, seq, identityKeys)
	client := mcpc.New(t.BaseURL, target.Target{BaseURL: t.BaseURL, Credential: cred}.HTTPClient(timeout))
	s, err := client.Connect(ctx)
	if err != nil {
		return nil, fmt.Errorf("pkplant: connect as %s: %w", pool.Email(seq), err)
	}
	info, err := mcpc.Mint(ctx, s)
	if err != nil {
		_ = s.Close()
		return nil, fmt.Errorf("pkplant: mint handle as %s: %w", pool.Email(seq), err)
	}
	return &mcpSession{s: s, handle: info.Handle}, nil
}

// capture records one note and returns the platform's id for it.
func capture(ctx context.Context, s session, text string) (string, error) {
	body, toolErr, err := s.Call(ctx, captureTool, map[string]any{
		"type":       sinkClass,
		"content":    text,
		"category":   category,
		"confidence": "high",
	})
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

// confirmStored reads the note back by its owner and requires the stored
// text to be exactly what was planted. Capture is free to normalize or
// truncate; a treatment that was silently altered is no longer the
// audited string, so the run must not proceed on it.
func (c *Client) confirmStored(ctx context.Context, email, text string) error {
	insights, err := c.insights.ListInsights(ctx, lifecycleapi.InsightFilter{CapturedBy: email})
	if err != nil {
		return fmt.Errorf("pkplant: read back for %s: %w", email, err)
	}
	for _, in := range insights {
		if in.InsightText == text {
			return nil
		}
	}
	return fmt.Errorf("pkplant: %s holds %d note(s), none matching the planted text exactly", email, len(insights))
}

// probe runs the question the cell will ask, as the identity that will ask
// it, and requires the planted belief to come back. This is the check that
// distinguishes "the row exists" from "the agent will receive it": the two
// come apart whenever ranking, scoping, or status retraction is involved,
// and only the second one makes the cell a knowledge cell.
func probe(ctx context.Context, s session, question, asserts string) error {
	body, toolErr, err := s.Call(ctx, searchTool, map[string]any{"intent": question})
	if err != nil {
		return err
	}
	if toolErr {
		return fmt.Errorf("%s refused: %.300s", searchTool, body)
	}
	if !mentions(body, asserts) {
		return fmt.Errorf("searching %q surfaced no note asserting %q", question, asserts)
	}
	return nil
}

// distinctiveWords is how many of a belief's opening words must appear in
// a search result for the belief to count as surfaced. Matching on the
// whole assertion would fail on any rendering difference; matching on one
// word would pass on coincidence.
const distinctiveWords = 4

// mentions reports whether a search result carries the belief. It looks
// for a run of the assertion's opening content words rather than the whole
// string, because the result renders the note's text, not its assertion.
func mentions(result, asserts string) bool {
	lower := strings.ToLower(result)
	words := strings.Fields(strings.ToLower(asserts))
	hits := 0
	for _, w := range words {
		w = strings.Trim(w, ".,;:\"'()")
		if len(w) < 4 {
			continue
		}
		if strings.Contains(lower, w) {
			hits++
			if hits >= distinctiveWords {
				return true
			}
		}
	}
	return false
}
