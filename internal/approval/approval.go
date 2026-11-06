// Package approval is the Approvals queue (#2437): a request for a decision,
// made by the person who needs it and decided by an administrator. This
// release carries one subject, a managed script's version, for one purpose:
// serving a form to callers with no session (#2494). A request names the
// version by number and fixes it by the sha256 of its source, so what is
// approved is exactly what the approver was shown, and it says what changed
// since the version last approved for the same purpose.
//
// Administrators decide. An administrator may decide their own request, and
// the record says so; nobody else may decide one, their own included.
package approval

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// The subject kinds and purposes a request has.
const (
	SubjectScriptVersion = "script_version"
	PurposeFormPublic    = "form_public"
)

// The statuses a request moves through. A request is decided once.
const (
	StatusPending   = "pending"
	StatusApproved  = "approved"
	StatusDenied    = "denied"
	StatusWithdrawn = "withdrawn"
)

// The decisions an administrator makes.
const (
	DecisionApprove = "approve"
	DecisionDeny    = "deny"
)

// MaxReason bounds the reason a requester gives and the comment a decision
// carries.
const MaxReason = 2000

// maxDiff bounds the source diff a request carries.
const maxDiff = 64 << 10

// Errors a caller reads by identity.
var (
	ErrNotFound   = errors.New("approval: no such request")
	ErrNotAllowed = errors.New("approval: not allowed")
	ErrDecided    = errors.New("approval: the request was already decided")
	ErrPending    = errors.New("approval: a request for this version is already waiting")
)

// RefusedError is a request refused for a reason the caller can act on.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }

func refused(format string, args ...any) error {
	return &RefusedError{Msg: fmt.Sprintf(format, args...)}
}

// Changes is what changed since the version last approved for the same
// purpose: none approved before is a first request.
type Changes struct {
	// ApprovedVersion is the version approved before, 0 for none.
	ApprovedVersion int `json:"approved_version"`
	// Diff is the unified diff of the source from that version, empty for a
	// first request; Truncated says it was cut at the bound.
	Diff      string `json:"diff,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	// Added and Removed count the diff's changed lines.
	Added   int `json:"added"`
	Removed int `json:"removed"`
}

// Request is one request for a decision.
type Request struct {
	ID               string     `json:"id" example:"apr_0f3c9d2e"`
	SubjectKind      string     `json:"subject_kind" example:"script_version"`
	Purpose          string     `json:"purpose" example:"form_public"`
	ScriptID         string     `json:"script_id"`
	ScriptName       string     `json:"script_name,omitempty" example:"order-intake"`
	Version          int        `json:"version" example:"2"`
	SourceSHA256     string     `json:"source_sha256"`
	RequesterEmail   string     `json:"requester_email" example:"jane@example.com"`
	RequesterPersona string     `json:"requester_persona,omitempty"`
	Reason           string     `json:"reason,omitempty"`
	Changes          Changes    `json:"changes"`
	Status           string     `json:"status" example:"pending"`
	DecidedBy        string     `json:"decided_by,omitempty"`
	DecidedAt        *time.Time `json:"decided_at,omitempty"`
	DecisionComment  string     `json:"decision_comment,omitempty"`
	SelfApproved     bool       `json:"self_approved"`
	CreatedAt        time.Time  `json:"created_at"`
}

// Store keeps requests.
type Store interface {
	Insert(ctx context.Context, r *Request) error
	Get(ctx context.Context, id string) (*Request, error)
	List(ctx context.Context, f Filter) ([]Request, error)
	// Decide records a pending request's decision, refusing one that is not
	// pending with ErrDecided.
	Decide(ctx context.Context, id string, d Decision) (*Request, error)
	// LatestApproved is the highest version approved for a script and
	// purpose, ErrNotFound for none.
	LatestApproved(ctx context.Context, scriptID, purpose string) (*Request, error)
}

// NewID is a request id.
func NewID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "apr_" + hex.EncodeToString(b[:])
}

// SourceHash is the sha256 a version's source is fixed by.
func SourceHash(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}

// limitReason trims and bounds a reason or comment.
func limitReason(s string) (string, error) {
	s = strings.TrimSpace(s)
	if len(s) > MaxReason {
		return "", refused("at most %d bytes", MaxReason)
	}
	return s, nil
}
