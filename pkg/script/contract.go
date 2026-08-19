package script

import (
	"fmt"
	"strings"
	"time"
)

// Contract is what a reference to a script resolves to: everything a caller
// needs to answer "what is this, may I use it, and what does it produce"
// without reading a line of Starlark.
//
// It exists as one type because a script is reachable from more than one
// surface — fetch on an mcp:script:<id> reference, and a prompt that attaches
// one (#1302, #1289) — and each surface answering the question in its own shape
// would be two contracts to keep in step. The source is deliberately not part of
// it: reading the code is what manage_script's get is for, and what a reviewer
// does.
//
// Two fields deserve their reasoning stated. Params are the APPROVED version's
// whenever there is one, because run_script executes that version and binds
// against its contract; the live record's parameters would describe an edit
// nothing will run. Approval carries both halves of the execution gate: whether
// a version is approved, and whether a run requested right now would be
// admitted at all, which a disabled or deprecated script fails even with an
// approved version behind it.
type Contract struct {
	ID          string   `json:"id" example:"script_a1b2c3d4"`
	Name        string   `json:"name" example:"daily-sales-report"`
	DisplayName string   `json:"display_name,omitempty" example:"Daily Sales Report"`
	Description string   `json:"description,omitempty" example:"Summarize yesterday's sales by region"`
	OwnerEmail  string   `json:"owner_email,omitempty" example:"jane@example.com"`
	Scope       string   `json:"scope" example:"personal"`
	Personas    []string `json:"personas,omitempty" example:"analyst"`
	Category    string   `json:"category,omitempty" example:"reporting"`
	Tags        []string `json:"tags,omitempty" example:"sales,reporting"`
	Status      string   `json:"status" example:"active"`
	Enabled     bool     `json:"enabled" example:"true"`

	// Params is the typed parameter contract a run binds against: the approved
	// version's when one is approved, the live record's otherwise.
	Params []Param `json:"params"`

	Approval ContractApproval `json:"approval"`

	// Schedule is the cadence this script fires on, nil when it has none.
	Schedule *ContractSchedule `json:"schedule,omitempty"`

	// LastRun is the most recent successful run, nil when the script has never
	// completed one. It answers "what does this produce" with evidence rather
	// than with a promise.
	LastRun *ContractRun `json:"last_successful_run,omitempty"`
}

// ContractApproval is the execution gate as a caller sees it.
type ContractApproval struct {
	// Approved reports whether a version is approved for execution.
	Approved bool `json:"approved" example:"true"`
	// Version is the approved version number, zero when none is approved.
	Version int `json:"version,omitempty" example:"3"`
	// ApprovedBy and ApprovedAt stamp who admitted that version and when.
	ApprovedBy string     `json:"approved_by,omitempty" example:"admin@example.com"`
	ApprovedAt *time.Time `json:"approved_at,omitempty"`
	// Automatic reports that the platform approved this version itself, because
	// the script is personal and its owner wrote it (#1367). Nobody reviewed it,
	// and a reader of the contract is told so rather than reading ApprovedBy as
	// a decision somebody made.
	Automatic bool `json:"automatic,omitempty" example:"false"`

	// Refusal states why a run requested now would be refused, and is empty when
	// one would be admitted. It is the gate's own answer (RefuseNewRun), not a
	// second reading of it, so a caller is never told a script is runnable that
	// run_script would then decline.
	Refusal string `json:"refusal,omitempty" example:"the script has no approved version, so nothing may execute it"`
}

// ContractSchedule is a script's cadence, reported rather than offered: a
// caller learns the script refreshes itself and when it next will, which is
// what decides whether to run it again or read what it already produced.
type ContractSchedule struct {
	CronSpec string `json:"cron_spec" example:"0 7 * * 1-5"`
	Timezone string `json:"timezone" example:"America/Los_Angeles"`
	Enabled  bool   `json:"enabled" example:"true"`
	// NextRunAt is the next due fire, nil when the schedule is disabled or its
	// expression has no further fire.
	NextRunAt *time.Time `json:"next_run_at,omitempty"`
}

// ContractRun summarizes one successful run and what it wrote.
type ContractRun struct {
	RunID      string           `json:"run_id" example:"run_a1b2c3d4"`
	Version    int              `json:"version" example:"3"`
	FinishedAt *time.Time       `json:"finished_at,omitempty"`
	Outputs    []ContractOutput `json:"outputs,omitempty"`
}

// Output kinds. An output has two shapes since external delivery (#1288), and
// the payload names which one it is rather than leaving a caller to infer it
// from which fields happen to be populated.
const (
	// OutputKindAsset is a portal asset the platform versions and serves.
	OutputKindAsset = "portal_asset"
	// OutputKindObject is an object delivered to a granted bucket, which the
	// platform wrote and does not hold.
	OutputKindObject = "object"
)

// ContractOutput is one thing a run produced. Kind decides which locator is
// meaningful: an asset carries the id and version a caller can fetch, an object
// carries the bucket and key it was written to, which is all the platform knows
// about it — the bytes left the platform and nothing here will serve them back.
type ContractOutput struct {
	Name string `json:"name" example:"sales_by_region"`
	Kind string `json:"kind" example:"portal_asset"`
	// Destination is the granted destination the output went to; "portal" for an
	// output that named none.
	Destination string `json:"destination" example:"portal"`
	Format      string `json:"format,omitempty" example:"csv"`
	RowCount    int    `json:"row_count,omitempty" example:"1420"`
	Bytes       int    `json:"bytes,omitempty" example:"98304"`

	// AssetID and AssetVersion locate an OutputKindAsset output.
	AssetID      string `json:"asset_id,omitempty" example:"asset_a1b2c3d4"`
	AssetVersion int    `json:"asset_version,omitempty" example:"7"`

	// Bucket and Key locate an OutputKindObject output.
	Bucket string `json:"bucket,omitempty" example:"acme-exports"`
	Key    string `json:"key,omitempty" example:"weekly/2026/08/sales.csv"`
}

// Title is the script's human label: its display name, falling back to the name
// an agent would call it by.
func (c Contract) Title() string {
	if c.DisplayName != "" {
		return c.DisplayName
	}
	return c.Name
}

// Text renders the contract as prose: what the script is, what it takes,
// whether anything will execute it, on what cadence, and what it last produced.
//
// It lives here rather than in a consumer because every surface that resolves a
// script reference shows the same document — fetch returns it as a document
// body, and a prompt that references a script serves it inline — and two
// renderers would be two answers to one question, drifting apart the first time
// either is edited.
func (c Contract) Text() string {
	parts := []string{c.Title()}
	if c.Description != "" {
		parts = append(parts, c.Description)
	}
	if names := ParamSummary(c.Params); names != "" {
		parts = append(parts, "Parameters: "+names)
	}
	parts = append(parts, c.approvalLine())
	if c.Schedule != nil {
		parts = append(parts, fmt.Sprintf("Schedule: %s (%s)%s",
			c.Schedule.CronSpec, c.Schedule.Timezone, c.Schedule.stateSuffix()))
	}
	parts = append(parts, c.LastRun.line())
	return strings.Join(parts, "\n")
}

// approvalLine states the execution gate in one line, naming the refusal when
// there is one so a reader is never left to infer it.
func (c Contract) approvalLine() string {
	line := "Approval: no version is approved, so nothing will execute this script."
	switch {
	case c.Approval.Approved && c.Approval.Automatic:
		line = fmt.Sprintf(
			"Approval: version %d, approved automatically because %s owns it and wrote it; nobody reviewed it.",
			c.Approval.Version, c.Approval.ApprovedBy)
	case c.Approval.Approved:
		line = fmt.Sprintf("Approval: version %d, approved by %s.", c.Approval.Version, c.Approval.ApprovedBy)
	}
	if c.Approval.Refusal != "" {
		return line + " A run requested now would be refused: " + c.Approval.Refusal + "."
	}
	return line
}

// stateSuffix reports a disabled cadence or an expression with nothing left to
// fire, either of which is otherwise indistinguishable from a schedule that
// simply has not fired yet.
func (s *ContractSchedule) stateSuffix() string {
	switch {
	case !s.Enabled:
		return ", disabled"
	case s.NextRunAt == nil:
		return ", no further fire"
	default:
		return ", next fire " + s.NextRunAt.UTC().Format(contractTimeLayout)
	}
}

// contractTimeLayout renders the instants in a contract. UTC throughout: a
// schedule states its own timezone next to its expression, and every other
// instant is a fact about when something happened.
const contractTimeLayout = "2006-01-02 15:04 UTC"

// line summarizes the last successful run and what it wrote, naming each
// output's shape: a portal asset the platform still serves, or an object
// delivered to a bucket, which the platform wrote and does not hold.
func (r *ContractRun) line() string {
	if r == nil {
		return "Last successful run: none — this script has never produced anything."
	}
	line := fmt.Sprintf("Last successful run: version %d", r.Version)
	if r.FinishedAt != nil {
		line += ", finished " + r.FinishedAt.UTC().Format(contractTimeLayout)
	}
	if len(r.Outputs) == 0 {
		return line + ", no recorded output."
	}
	outs := make([]string, 0, len(r.Outputs))
	for _, o := range r.Outputs {
		outs = append(outs, o.Name+" ("+o.location()+")")
	}
	return line + ". Outputs: " + strings.Join(outs, ", ") + "."
}

// location renders where one output landed.
func (o ContractOutput) location() string {
	switch o.Kind {
	case OutputKindAsset:
		return fmt.Sprintf("portal asset %s v%d", o.AssetID, o.AssetVersion)
	case OutputKindObject:
		return fmt.Sprintf("object %s/%s delivered to %s", o.Bucket, o.Key, o.Destination)
	default:
		return "destination " + o.Destination
	}
}

// ParamSummary renders a parameter contract as a comma-separated name list,
// marking the required ones, so a caller learns what a script needs without
// reading the typed contract field by field. Empty for a script that takes no
// parameters.
func ParamSummary(params []Param) string {
	if len(params) == 0 {
		return ""
	}
	names := make([]string, 0, len(params))
	for _, p := range params {
		if p.Required {
			names = append(names, p.Name+" (required)")
			continue
		}
		names = append(names, p.Name)
	}
	return strings.Join(names, ", ")
}

// VisibleToAny reports whether a caller who belongs to any of personas may see
// the script this contract describes. It answers through the same rule the
// record and the store predicate answer through, so a surface holding only the
// contract — the fetch path, which composes it in one read — enforces the
// identical visibility without a second read of the script row.
func (c Contract) VisibleToAny(email string, personas []string) bool {
	return scopeVisibleToAny(c.Scope, c.Personas, c.OwnerEmail, email, personas)
}

// BuildContract renders the contract for one script from the records that
// define it: the live row, the approved version (nil when none is approved),
// the schedule (nil when it has none), and the last successful run (nil when it
// has never had one).
//
// It is a pure function over records the caller has already read, so every
// surface that resolves a script reference produces the identical document and
// none of them needs its own composition rule.
func BuildContract(sc *Script, approved *Version, sched *Schedule, lastRun *Run) Contract {
	if sc == nil {
		return Contract{}
	}
	c := Contract{
		ID:          sc.ID,
		Name:        sc.Name,
		DisplayName: sc.DisplayName,
		Description: sc.Description,
		OwnerEmail:  sc.OwnerEmail,
		Scope:       sc.Scope,
		Personas:    sc.Personas,
		Category:    sc.Category,
		Tags:        sc.Tags,
		Status:      sc.Status,
		Enabled:     sc.Enabled,
		Params:      sc.Params,
		Approval:    contractApproval(sc, approved),
	}
	if approved != nil {
		// The parameter contract a run binds against belongs to the version that
		// will execute, not to a live edit awaiting review.
		c.Params = approved.Params
	}
	if sched != nil {
		c.Schedule = contractSchedule(sched)
	}
	if lastRun != nil {
		c.LastRun = contractRun(lastRun)
	}
	return c
}

// contractApproval renders the execution gate for the contract.
func contractApproval(sc *Script, approved *Version) ContractApproval {
	out := ContractApproval{Refusal: refusalText(RefuseNewRun(sc, approved))}
	if approved == nil || !approved.Approved() {
		return out
	}
	out.Approved = true
	out.Version = approved.Version
	out.ApprovedBy = approved.ApprovedBy
	out.ApprovedAt = approved.ApprovedAt
	out.Automatic = approved.AutoApproved
	return out
}

// refusalText renders a gate refusal as its message, or "" when the gate
// admits the run.
func refusalText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// contractSchedule renders a schedule's cadence. NextRunAt is reported only
// while the schedule is enabled and has a further fire: a disabled schedule's
// stored due time is a leftover, not a prediction.
func contractSchedule(s *Schedule) *ContractSchedule {
	out := &ContractSchedule{
		CronSpec: s.CronSpec,
		Timezone: s.Timezone,
		Enabled:  s.Enabled,
	}
	if due := s.DueAt(); !due.IsZero() {
		out.NextRunAt = &due
	}
	return out
}

// contractRun renders one run and its outputs.
func contractRun(r *Run) *ContractRun {
	out := &ContractRun{RunID: r.ID, Version: r.Version, FinishedAt: r.FinishedAt}
	out.Outputs = make([]ContractOutput, 0, len(r.Outputs))
	for _, o := range r.Outputs {
		out.Outputs = append(out.Outputs, contractOutput(o))
	}
	if len(out.Outputs) == 0 {
		out.Outputs = nil
	}
	return out
}

// contractOutput renders one recorded output, naming its shape from what was
// actually written rather than from the destination's name: a granted
// destination may be named anything, and the locator that is populated is the
// only unambiguous evidence of where the bytes went.
func contractOutput(o RunOutput) ContractOutput {
	out := ContractOutput{
		Name:         o.Name,
		Destination:  destinationOf(o),
		Format:       o.Format,
		RowCount:     o.RowCount,
		Bytes:        o.Bytes,
		AssetID:      o.AssetID,
		AssetVersion: o.AssetVersion,
		Bucket:       o.Bucket,
		Key:          o.Key,
	}
	switch {
	case o.AssetID != "":
		out.Kind = OutputKindAsset
	case o.Bucket != "":
		out.Kind = OutputKindObject
	}
	return out
}
