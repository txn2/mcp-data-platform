package pollutionplant

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/bench/internal/pool"
	"github.com/txn2/mcp-data-platform/bench/internal/promote"
)

// The RQ3 remediations. The field validates that governance machinery works
// mechanically; this study asks whether belief recovers, which needs the
// remediation driven through the surfaces an operator actually has and the
// resulting state read per channel rather than assumed.
//
// The two mechanisms do NOT retract the same channels, and the difference is
// a property of the platform, verified in its source rather than inferred:
//
//   - Rollback reverts the applied change at its sink and marks the source
//     insights rolled back, so both the sink and the insight channel stop
//     carrying the claim.
//   - Supersede retracts the insight only. An applied insight cannot be
//     superseded through the status API (the transition table admits
//     applied -> rolled_back and nothing else); what supersedes it is the
//     capturing identity restating the claim, which the recall-first check
//     matches and supersedes. The change that was already applied to the
//     sink stays applied.
//
// So supersede is a partial retraction, and a study that treated the two as
// interchangeable would attribute a difference in recovery to the mechanism
// when it was really a difference in how much of the claim was withdrawn.
// Result reports each channel separately for exactly that reason.

// Remediation names how a planted claim is retracted.
type Remediation string

const (
	// RemediationSupersede has the capturing identity restate the claim
	// correctly, which supersedes the stored insight.
	RemediationSupersede Remediation = "supersede"
	// RemediationRollback reverts the applied changeset.
	RemediationRollback Remediation = "rollback"
)

// Insight statuses this package reads back. They mirror the platform's
// lifecycle constants; promote already names the ones it drives.
const statusSuperseded = promote.StatusSuperseded

// retractedStatuses are the insight statuses the platform treats as no
// longer in force, and therefore stops delivering. Mirrors
// knowledge.isLiveInsightStatus.
var retractedStatuses = []string{"rejected", statusSuperseded, "rolled_back"}

// RemediateRequest is one remediation of one planted claim.
type RemediateRequest struct {
	// Remediation selects the mechanism.
	Remediation Remediation
	// Planted is the plant's own result: the claim, its insight, and its
	// changeset.
	Planted Result
	// Treatment is the claim that was planted.
	Treatment Treatment
	// Correction is the claim's correct counterpart, restated by the
	// capturing identity for the supersede mechanism. Unused by rollback.
	Correction Treatment
	// TeacherSeq is the identity that captured the claim. The supersede
	// mechanism must run as this identity: the recall-first check that
	// detects a restatement is scoped to the caller's own memory, so a
	// correction captured by anyone else supersedes nothing.
	TeacherSeq int
	// WitnessSeq is a fresh identity the post-remediation reachability is
	// read as.
	WitnessSeq int
	// GateIntent opens the search-first gate; DefaultGateIntent when empty.
	GateIntent string
}

// RemediateResult is the state of the claim after the remediation, read per
// channel through the platform's own APIs.
type RemediateResult struct {
	// Remediation is the mechanism that ran.
	Remediation Remediation `json:"remediation"`
	// InsightStatus is the planted insight's status afterwards.
	InsightStatus string `json:"insight_status"`
	// InsightRetracted is true when that status is one the platform stops
	// delivering.
	InsightRetracted bool `json:"insight_retracted"`
	// CorrectionInsightID is the corrective capture's id (supersede only).
	CorrectionInsightID string `json:"correction_insight_id,omitempty"`
	// InSearch and InSink are the same reachability read the plant took,
	// repeated afterwards as a fresh identity. They are what the study
	// actually needs: a status column says what the platform recorded, and
	// these say what an evaluator would still be handed.
	InSearch bool `json:"in_search"`
	InSink   bool `json:"in_sink"`
}

// Remediate retracts a planted claim and reports what each channel carries
// afterwards. A mechanism that failed to retract the channel it is defined
// to retract is an error: an arm that ran on a claim still in force would
// measure the planted condition under a remediated label.
func (c *Client) Remediate(ctx context.Context, req RemediateRequest) (RemediateResult, error) {
	res := RemediateResult{Remediation: req.Remediation}
	if err := checkRemediateRequest(req); err != nil {
		return res, err
	}
	var err error
	switch req.Remediation {
	case RemediationSupersede:
		res.CorrectionInsightID, err = c.supersede(ctx, req)
	case RemediationRollback:
		err = c.rollback(ctx, req)
	default:
		err = fmt.Errorf("pollutionplant: unknown remediation %q", req.Remediation)
	}
	if err != nil {
		return res, err
	}
	if err := c.readRemediatedState(ctx, req, &res); err != nil {
		return res, err
	}
	return res, checkRetraction(req, res)
}

// checkRemediateRequest refuses a remediation that could not do what it
// claims.
func checkRemediateRequest(req RemediateRequest) error {
	if err := req.Treatment.Validate(); err != nil {
		return err
	}
	switch {
	case req.Planted.InsightID == "":
		return errors.New("pollutionplant: remediation needs the planted insight id")
	case req.TeacherSeq < 1 || req.WitnessSeq < 1:
		return fmt.Errorf("pollutionplant: remediation needs pool identities, got teacher %d witness %d",
			req.TeacherSeq, req.WitnessSeq)
	case req.Remediation == RemediationRollback && req.Planted.ChangesetID == "":
		return errors.New("pollutionplant: rollback needs the changeset the apply recorded; the plant result carries none")
	case req.Remediation == RemediationSupersede:
		return checkCorrection(req)
	}
	return nil
}

// checkCorrection requires the correction to be the treatment's counterpart:
// the same claim about the same entity, corrected. A correction about
// something else would not be matched as a restatement, and the supersede
// would silently no-op.
func checkCorrection(req RemediateRequest) error {
	if err := req.Correction.Validate(); err != nil {
		return err
	}
	switch {
	case req.Correction.Arm != ArmCorrect:
		return fmt.Errorf("pollutionplant: the correction must be the correct arm, got %s", req.Correction.Arm)
	case req.Correction.Fixture != req.Treatment.Fixture || req.Correction.Class != req.Treatment.Class:
		return fmt.Errorf("pollutionplant: correction %s is not the counterpart of treatment %s",
			req.Correction.ID, req.Treatment.ID)
	case req.Correction.EntityURN != req.Treatment.EntityURN:
		return fmt.Errorf("pollutionplant: correction %s anchors to %q but the treatment anchors to %q; "+
			"the restatement check is entity-scoped, so a different anchor supersedes nothing",
			req.Correction.ID, req.Correction.EntityURN, req.Treatment.EntityURN)
	}
	return nil
}

// supersede restates the claim correctly as the identity that captured it,
// which is what the recall-first check matches. It returns the corrective
// capture's insight id.
func (c *Client) supersede(ctx context.Context, req RemediateRequest) (string, error) {
	email := pool.Email(req.TeacherSeq)
	teacher, err := c.dial(ctx, req.TeacherSeq)
	if err != nil {
		return "", fmt.Errorf("pollutionplant: connect as %s: %w", email, err)
	}
	defer func() { _ = teacher.Close() }()
	if err := openGate(ctx, teacher, req.GateIntent); err != nil {
		return "", err
	}
	id, err := capture(ctx, teacher, req.Correction)
	if err != nil {
		return "", fmt.Errorf("pollutionplant: corrective capture as %s: %w", email, err)
	}
	if c.log != nil {
		c.log.Info("corrective capture", "insight_id", id, "teacher", email, "correction", req.Correction.ID)
	}
	return id, nil
}

// rollback reverts the applied changeset over a reviewer session, through
// the same tool an operator would use.
func (c *Client) rollback(ctx context.Context, req RemediateRequest) error {
	admin, err := c.dial(ctx, AdminSeq)
	if err != nil {
		return fmt.Errorf("pollutionplant: connect as reviewer: %w", err)
	}
	defer func() { _ = admin.Close() }()
	if err := openGate(ctx, admin, req.GateIntent); err != nil {
		return err
	}
	body, toolErr, err := admin.Call(ctx, applyTool, map[string]any{
		"action":       "rollback",
		"changeset_id": req.Planted.ChangesetID,
		"confirm":      true,
	})
	if err != nil {
		return fmt.Errorf("pollutionplant: rollback transport: %w", err)
	}
	if toolErr {
		return fmt.Errorf("pollutionplant: rollback of changeset %s refused: %.300s", req.Planted.ChangesetID, body)
	}
	var out struct {
		ChangesetID string `json:"changeset_id"`
	}
	if err := json.Unmarshal([]byte(body), &out); err != nil || out.ChangesetID != req.Planted.ChangesetID {
		return fmt.Errorf("pollutionplant: rollback of changeset %s returned no matching confirmation: %.300s",
			req.Planted.ChangesetID, body)
	}
	if c.log != nil {
		c.log.Info("rolled back", "changeset_id", req.Planted.ChangesetID)
	}
	return nil
}

// readRemediatedState reads the claim's status and its reachability for a
// fresh identity.
func (c *Client) readRemediatedState(ctx context.Context, req RemediateRequest, res *RemediateResult) error {
	in, err := c.insights.GetInsight(ctx, req.Planted.InsightID)
	if err != nil {
		return fmt.Errorf("pollutionplant: read insight %s after %s: %w", req.Planted.InsightID, req.Remediation, err)
	}
	res.InsightStatus = in.Status
	res.InsightRetracted = slices.Contains(retractedStatuses, in.Status)

	email := pool.Email(req.WitnessSeq)
	w, err := c.dial(ctx, req.WitnessSeq)
	if err != nil {
		return fmt.Errorf("pollutionplant: connect as witness %s: %w", email, err)
	}
	defer func() { _ = w.Close() }()
	res.InSearch, res.InSink, err = c.reach(ctx, w, req.Treatment, req.GateIntent)
	if err != nil {
		return err
	}
	if c.log != nil {
		c.log.Info("post-remediation state", "remediation", req.Remediation, "insight_status", res.InsightStatus,
			"in_search", res.InSearch, "in_sink", res.InSink, "witness", email)
	}
	return nil
}

// checkRetraction holds each mechanism to what it is defined to retract,
// and only to that.
//
// Both must retract the insight: that is the channel both mechanisms act
// on, and a claim still at a delivered status means the mechanism did not
// take (for supersede, most likely because the correction was not similar
// enough to the claim for the recall-first check to match it, which would
// leave the arm running on the planted condition under a remediated label).
//
// Only rollback is held to the sink and to reachability. Supersede leaves
// the applied change in place by design, and where search federates the
// catalog the claim can still be surfaced from there — so a supersede arm
// records what remains reachable rather than asserting it is gone. Treating
// that residue as a failure would have the harness refuse the very state
// RQ3 exists to measure.
func checkRetraction(req RemediateRequest, res RemediateResult) error {
	if !res.InsightRetracted {
		return fmt.Errorf("pollutionplant: %s left insight %s at status %q, which the platform still delivers; "+
			"the arm would run on a claim still in force", req.Remediation, req.Planted.InsightID, res.InsightStatus)
	}
	if req.Remediation != RemediationRollback {
		return nil
	}
	if res.InSink {
		return fmt.Errorf("pollutionplant: rollback of changeset %s left the applied change readable at its sink; "+
			"the revert did not take", req.Planted.ChangesetID)
	}
	if res.InSearch {
		return fmt.Errorf("pollutionplant: rollback of changeset %s left the claim reachable through search for a "+
			"fresh identity (needle %q); the retraction did not take", req.Planted.ChangesetID, req.Treatment.Needle)
	}
	return nil
}
