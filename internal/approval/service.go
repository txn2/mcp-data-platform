package approval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/script"
	"github.com/txn2/mcp-data-platform/pkg/textpatch"
)

// diffContext is the lines of context the changes diff keeps.
const diffContext = 3

// Scripts reads the scripts and versions a request names.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
	GetByName(ctx context.Context, ownerEmail, name string) (*script.Script, error)
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Notifier tells approvers a request is waiting and a requester how theirs
// was decided. Its failures are its own: a request stands whether or not the
// mail went out.
type Notifier interface {
	Requested(ctx context.Context, r *Request, approvers []string)
	Decided(ctx context.Context, r *Request)
}

// Actor is who is asking.
type Actor struct {
	Email   string
	Persona string
	Admin   bool
}

// Service is the queue's operations.
type Service struct {
	Store   Store
	Scripts Scripts
	// IsForm reports whether a version's source defines a form; a
	// form_public request names a form's version.
	IsForm func(source string) bool
	// Approvers lists the administrators' addresses; nil notifies nobody.
	Approvers func(ctx context.Context) []string
	// Notifier sends the two notices; nil sends none.
	Notifier Notifier
	// Now is the clock; nil is time.Now.
	Now func() time.Time
}

// Submit is what a requester asks for.
type Submit struct {
	// Script is the script's id, or the name of one of the requester's own.
	Script  string
	Version int
	Purpose string
	Reason  string
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// ResolveScript reads the script a request names: by id, or by the name of
// one of the actor's own scripts.
func (s *Service) ResolveScript(ctx context.Context, actor Actor, ref string) (*script.Script, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, refused("name the script: its id, or the name of one of your scripts")
	}
	sc, err := s.Scripts.GetByID(ctx, ref)
	if (err != nil || sc == nil) && actor.Email != "" {
		sc, err = s.Scripts.GetByName(ctx, actor.Email, ref)
	}
	if err != nil || sc == nil {
		return nil, refused("no script %q: name it by id, or by the name of one of your scripts", ref)
	}
	return sc, nil
}

// Submit files a request for actor: the script's owner, or an
// administrator, asking for one of its versions.
func (s *Service) Submit(ctx context.Context, actor Actor, in Submit) (*Request, error) {
	reason, err := checkSubmit(actor, &in)
	if err != nil {
		return nil, err
	}
	sc, v, err := s.subject(ctx, actor, in)
	if err != nil {
		return nil, err
	}
	r := &Request{
		ID: NewID(), SubjectKind: SubjectScriptVersion, Purpose: in.Purpose, ScriptID: sc.ID, ScriptName: sc.Name,
		Version: v.Version, SourceSHA256: SourceHash(v.Source), RequesterEmail: actor.Email,
		RequesterPersona: actor.Persona, Reason: reason,
	}
	if r.Changes, err = s.changesSince(ctx, sc.ID, in.Purpose, v); err != nil {
		return nil, err
	}
	if err := s.Store.Insert(ctx, r); err != nil {
		if errors.Is(err, ErrPending) {
			return nil, refused("a request for version %d of %s is already waiting for an administrator", v.Version, sc.Name)
		}
		return nil, fmt.Errorf("filing the request: %w", err)
	}
	if s.Notifier != nil && s.Approvers != nil {
		s.Notifier.Requested(ctx, r, s.Approvers(ctx))
	}
	return r, nil
}

// checkSubmit refuses a request that names no person or no purpose this
// platform decides, defaulting the purpose, and returns its reason trimmed.
func checkSubmit(actor Actor, in *Submit) (string, error) {
	if actor.Email == "" {
		return "", refused("a request belongs to a person: sign in with an account that has an email")
	}
	if in.Purpose == "" {
		in.Purpose = PurposeFormPublic
	}
	if in.Purpose != PurposeFormPublic {
		return "", refused("purpose %q is not one this platform decides; the one purpose is %s", in.Purpose, PurposeFormPublic)
	}
	reason, err := limitReason(in.Reason)
	if err != nil {
		return "", refused("reason: at most %d bytes", MaxReason)
	}
	return reason, nil
}

// subject reads the script and version a request names, refusing anyone but
// its owner or an administrator and a version that is not a form.
func (s *Service) subject(ctx context.Context, actor Actor, in Submit) (*script.Script, *script.Version, error) {
	sc, err := s.ResolveScript(ctx, actor, in.Script)
	if err != nil {
		return nil, nil, err
	}
	if !actor.Admin && !sc.OwnedBy(actor.Email) {
		return nil, nil, fmt.Errorf("only the script's owner or an administrator asks for one of its versions: %w", ErrNotAllowed)
	}
	version := in.Version
	if version <= 0 {
		version = sc.Version
	}
	v, err := s.Scripts.GetVersion(ctx, sc.ID, version)
	if err != nil || v == nil {
		return nil, nil, refused("%s has no version %d", sc.Name, version)
	}
	if s.IsForm != nil && !s.IsForm(v.Source) {
		return nil, nil, refused("version %d of %s is not a form: a form defines form_spec(req) and form_submit(req, values)", v.Version, sc.Name)
	}
	return sc, v, nil
}

// changesSince is what changed from the version last approved for purpose to
// v.
func (s *Service) changesSince(ctx context.Context, scriptID, purpose string, v *script.Version) (Changes, error) {
	prev, err := s.Store.LatestApproved(ctx, scriptID, purpose)
	if errors.Is(err, ErrNotFound) {
		return Changes{}, nil
	}
	if err != nil {
		return Changes{}, fmt.Errorf("reading the version approved before: %w", err)
	}
	c := Changes{ApprovedVersion: prev.Version}
	old, err := s.Scripts.GetVersion(ctx, scriptID, prev.Version)
	if err != nil || old == nil {
		// The approved version was deleted with its history: say which it
		// was and show no diff rather than refuse the request.
		return c, nil //nolint:nilerr // reported as a change with no diff
	}
	diff := textpatch.UnifiedDiffLabeled(old.Source, v.Source,
		fmt.Sprintf("version %d (approved)", old.Version), fmt.Sprintf("version %d", v.Version), diffContext)
	for line := range strings.SplitSeq(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++"), strings.HasPrefix(line, "---"):
		case strings.HasPrefix(line, "+"):
			c.Added++
		case strings.HasPrefix(line, "-"):
			c.Removed++
		}
	}
	if len(diff) > maxDiff {
		diff, c.Truncated = diff[:maxDiff], true
	}
	c.Diff = diff
	return c, nil
}

// List is the requests actor may read: an administrator's every one, anyone
// else's their own.
func (s *Service) List(ctx context.Context, actor Actor, f Filter) ([]Request, error) {
	if actor.Email == "" {
		return nil, refused("sign in with an account that has an email to read requests")
	}
	if !actor.Admin {
		f.Requester = actor.Email
	}
	return s.Store.List(ctx, f) //nolint:wrapcheck // the store's errors are read by identity
}

// Get is one request actor may read.
func (s *Service) Get(ctx context.Context, actor Actor, id string) (*Request, error) {
	r, err := s.Store.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if !actor.Admin && r.RequesterEmail != actor.Email {
		return nil, ErrNotFound
	}
	return r, nil
}

// Decide records an administrator's decision. A non-administrator is refused
// whatever the request, their own included; an administrator deciding their
// own request is recorded as self-approved.
func (s *Service) Decide(ctx context.Context, actor Actor, id, decision, comment string) (*Request, error) {
	if !actor.Admin {
		return nil, fmt.Errorf("administrators decide requests, including a request of your own: %w", ErrNotAllowed)
	}
	status, err := statusOf(decision)
	if err != nil {
		return nil, err
	}
	comment, err = limitReason(comment)
	if err != nil {
		return nil, refused("comment: at most %d bytes", MaxReason)
	}
	cur, err := s.Store.Get(ctx, id)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if status == StatusApproved {
		if err := s.stillTheSource(ctx, cur); err != nil {
			return nil, err
		}
	}
	return s.record(ctx, actor, cur, Decision{
		Status: status, By: actor.Email, Comment: comment,
		SelfApproved: status == StatusApproved && cur.RequesterEmail == actor.Email, At: s.now(),
	})
}

// record stores a decision on cur and tells its requester, unless the
// requester made it.
func (s *Service) record(ctx context.Context, actor Actor, cur *Request, d Decision) (*Request, error) {
	r, err := s.Store.Decide(ctx, cur.ID, d)
	if err != nil {
		return nil, err //nolint:wrapcheck // read by identity
	}
	if s.Notifier != nil && r.RequesterEmail != actor.Email {
		s.Notifier.Decided(ctx, r)
	}
	return r, nil
}

// statusOf is the status a decision moves a request to.
func statusOf(decision string) (string, error) {
	switch decision {
	case DecisionApprove:
		return StatusApproved, nil
	case DecisionDeny:
		return StatusDenied, nil
	}
	return "", refused("decision %q: approve or deny", decision)
}

// stillTheSource refuses approving a version whose source is no longer the
// one requested. Versions are kept as written, so this holds unless the
// history was rewritten under the request.
func (s *Service) stillTheSource(ctx context.Context, r *Request) error {
	if r.SubjectKind == SubjectSiteRelease {
		return nil // a release is immutable, and promoting compares the digest
	}
	v, err := s.Scripts.GetVersion(ctx, r.ScriptID, r.Version)
	if err != nil || v == nil {
		return refused("version %d of the script no longer exists; it cannot be approved", r.Version)
	}
	if SourceHash(v.Source) != r.SourceSHA256 {
		return refused("version %d's source is not the source that was requested; it cannot be approved", r.Version)
	}
	return nil
}

// Approved is the version approved for a script and purpose, 0 for none. A
// failure to read is logged and reads as none, which serves nothing publicly.
func (s *Service) Approved(ctx context.Context, scriptID, purpose string) int {
	if s == nil || s.Store == nil {
		return 0
	}
	r, err := s.Store.LatestApproved(ctx, scriptID, purpose)
	if errors.Is(err, ErrNotFound) {
		return 0
	}
	if err != nil {
		slog.WarnContext(ctx, "approvals: reading the approved version failed", "error", logsan.SanitizeForLog(err.Error()))
		return 0
	}
	return r.Version
}

// RecordSelfApproval records an administrator's approval of a version they
// serve publicly without a request: an administrator binding a form public
// approves the version served, and the record says it was self-approved.
// A version already approved records nothing.
func (s *Service) RecordSelfApproval(ctx context.Context, actor Actor, sc *script.Script, version int) error {
	if !actor.Admin {
		return ErrNotAllowed
	}
	if s.Approved(ctx, sc.ID, PurposeFormPublic) >= version {
		return nil
	}
	id, err := s.selfRequest(ctx, actor, sc, version)
	if err != nil {
		return err
	}
	_, err = s.Store.Decide(ctx, id, Decision{Status: StatusApproved, By: actor.Email, SelfApproved: true, At: s.now()})
	return err //nolint:wrapcheck // read by identity
}

// selfRequest files the request an administrator's binding decides, or names
// the one already waiting for the version.
func (s *Service) selfRequest(ctx context.Context, actor Actor, sc *script.Script, version int) (string, error) {
	v, err := s.Scripts.GetVersion(ctx, sc.ID, version)
	if err != nil || v == nil {
		return "", refused("%s has no version %d", sc.Name, version)
	}
	r := &Request{
		ID: NewID(), SubjectKind: SubjectScriptVersion, Purpose: PurposeFormPublic, ScriptID: sc.ID, ScriptName: sc.Name,
		Version: version, SourceSHA256: SourceHash(v.Source), RequesterEmail: actor.Email, RequesterPersona: actor.Persona,
		Reason: "served publicly by an administrator",
	}
	if r.Changes, err = s.changesSince(ctx, sc.ID, PurposeFormPublic, v); err != nil {
		return "", err
	}
	err = s.Store.Insert(ctx, r)
	if !errors.Is(err, ErrPending) {
		return r.ID, err //nolint:wrapcheck // read by identity
	}
	pending, err := s.Store.List(ctx, Filter{Status: StatusPending, ScriptID: sc.ID})
	if err != nil {
		return "", err //nolint:wrapcheck // read by identity
	}
	for _, p := range pending {
		if p.Version == version && p.Purpose == PurposeFormPublic {
			return p.ID, nil
		}
	}
	return "", ErrNotFound
}

// SubmitRelease names the site release a promotion waits on.
type SubmitRelease struct {
	ReleaseID string
	Site      string
	Number    int
	Target    string
	Digest    string
	Reason    string
}

func releaseRequest(actor Actor, in SubmitRelease, reason string) *Request {
	return &Request{
		ID: NewID(), SubjectKind: SubjectSiteRelease, Purpose: PurposeSitePromote, Version: in.Number,
		SourceSHA256: in.Digest, RequesterEmail: actor.Email, RequesterPersona: actor.Persona, Reason: reason,
		SiteReleaseID: in.ReleaseID, Target: in.Target, SiteName: in.Site,
	}
}

// SubmitRelease files a request to serve a site release in an environment
// that requires approval (#2518), or returns the one already waiting.
func (s *Service) SubmitRelease(ctx context.Context, actor Actor, in SubmitRelease) (*Request, error) {
	if actor.Email == "" {
		return nil, refused("a request belongs to a person: sign in with an account that has an email")
	}
	reason, err := limitReason(in.Reason)
	if err != nil {
		return nil, refused("reason: at most %d bytes", MaxReason)
	}
	r := releaseRequest(actor, in, reason)
	if err := s.Store.Insert(ctx, r); err != nil {
		if errors.Is(err, ErrPending) {
			return s.pendingRelease(ctx, in)
		}
		return nil, fmt.Errorf("filing the request: %w", err)
	}
	if s.Notifier != nil && s.Approvers != nil {
		s.Notifier.Requested(ctx, r, s.Approvers(ctx))
	}
	return r, nil
}

// pendingRelease is the request already waiting on a release and target.
func (s *Service) pendingRelease(ctx context.Context, in SubmitRelease) (*Request, error) {
	pending, err := s.Store.List(ctx, Filter{Status: StatusPending, SiteReleaseID: in.ReleaseID})
	if err != nil {
		return nil, fmt.Errorf("reading the waiting requests: %w", err)
	}
	for i := range pending {
		if pending[i].SiteReleaseID == in.ReleaseID && pending[i].Target == in.Target {
			return &pending[i], nil
		}
	}
	return nil, ErrPending
}

// ReleaseApproved reports whether a release is approved for a target at the
// digest. A failure to read is logged and reads as not approved, which
// promotes nothing.
func (s *Service) ReleaseApproved(ctx context.Context, releaseID, target, digest string) bool {
	ok, err := s.Store.ReleaseApproved(ctx, releaseID, target, digest)
	if err != nil {
		slog.WarnContext(ctx, "approvals: reading a release's approval failed", "error", logsan.SanitizeForLog(err.Error()))
		return false
	}
	return ok
}

// RecordReleaseSelfApproval records an administrator's approval of a release
// they promote themselves, and the record says it was self-approved.
func (s *Service) RecordReleaseSelfApproval(ctx context.Context, actor Actor, in SubmitRelease) error {
	if !actor.Admin {
		return ErrNotAllowed
	}
	r := releaseRequest(actor, in, "promoted by an administrator")
	id := r.ID
	switch err := s.Store.Insert(ctx, r); {
	case errors.Is(err, ErrPending):
		pending, perr := s.pendingRelease(ctx, in)
		if perr != nil {
			return perr
		}
		id = pending.ID
	case err != nil:
		return fmt.Errorf("recording the approval: %w", err)
	}
	_, err := s.Store.Decide(ctx, id, Decision{Status: StatusApproved, By: actor.Email, SelfApproved: true, At: s.now()})
	return err //nolint:wrapcheck // read by identity
}
