package approval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/txn2/mcp-data-platform/internal/logsan"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// RunAs is what a script's tool-call handlers run (#2436): the highest
// version approved for call_handler, and the administrator who approved it,
// whose roles it runs with.
type RunAs struct {
	Version  int
	Approver string
	Roles    []string
	// SelfApproved says the approver saved or bound it themselves.
	SelfApproved bool
}

// HandlerRunAs is what sc's tool-call handlers run; ok is false when no
// version is approved, and then nothing runs. A failure to read is logged
// and reads as none.
func (s *Service) HandlerRunAs(ctx context.Context, scriptID string) (RunAs, bool) {
	if s == nil || s.Store == nil || scriptID == "" {
		return RunAs{}, false
	}
	r, err := s.Store.LatestApproved(ctx, scriptID, PurposeCallHandler)
	if errors.Is(err, ErrNotFound) {
		return RunAs{}, false
	}
	if err != nil {
		slog.WarnContext(ctx, "approvals: reading a handler's approved version failed", "error", logsan.SanitizeForLog(err.Error()))
		return RunAs{}, false
	}
	return RunAs{Version: r.Version, Approver: r.DecidedBy, Roles: r.DecidedRoles, SelfApproved: r.SelfApproved}, true
}

// PendingHandlerVersion is the newest version of a script waiting for an
// administrator's approval for handlers, 0 for none.
func (s *Service) PendingHandlerVersion(ctx context.Context, scriptID string) int {
	if s == nil || s.Store == nil || scriptID == "" {
		return 0
	}
	pending, err := s.Store.List(ctx, Filter{Status: StatusPending, ScriptID: scriptID, Purpose: PurposeCallHandler, Limit: 1})
	if err != nil || len(pending) == 0 {
		return 0
	}
	return pending[0].Version
}

// ApproveForHandlers records an administrator's approval of a version of sc
// for its tool-call handlers (version 0 is the latest), with their roles,
// marked self-approved: an administrator saving or binding a handler's script
// approves what they saved or bound. A version already approved, or older
// than one approved, records nothing.
func (s *Service) ApproveForHandlers(ctx context.Context, actor Actor, sc *script.Script, version int) error {
	if !actor.Admin {
		return fmt.Errorf("an administrator approves a handler's version: %w", ErrNotAllowed)
	}
	if version <= 0 {
		version = sc.Version
	}
	if cur, ok := s.HandlerRunAs(ctx, sc.ID); ok && cur.Version >= version {
		return nil
	}
	id, err := s.handlerRequest(ctx, actor, sc, version, "approved for handlers by an administrator")
	if err != nil {
		return err
	}
	_, err = s.Store.Decide(ctx, id, Decision{Status: StatusApproved, By: actor.Email, SelfApproved: true, At: s.now(), Roles: actor.Roles})
	return err //nolint:wrapcheck // read by identity
}

// HandlerVersionSaved is what a save of a script holding tool-call handlers
// does: an administrator's save approves the version, anyone else's files a
// request an administrator decides, and the approved version keeps running
// meanwhile.
func (s *Service) HandlerVersionSaved(ctx context.Context, actor Actor, sc *script.Script, version int) error {
	if s == nil || s.Store == nil {
		return nil
	}
	if actor.Admin {
		return s.ApproveForHandlers(ctx, actor, sc, version)
	}
	id, err := s.handlerRequest(ctx, actor, sc, version, "a new version of a script that decides, changes or follows other people's calls")
	if err != nil {
		return err
	}
	r, err := s.Store.Get(ctx, id)
	if err == nil && s.Notifier != nil && s.Approvers != nil {
		s.Notifier.Requested(ctx, r, s.Approvers(ctx))
	}
	return nil
}

// handlerRequest files a call_handler request for version of sc, or names the
// one already waiting for it.
func (s *Service) handlerRequest(ctx context.Context, actor Actor, sc *script.Script, version int, reason string) (string, error) {
	v, err := s.Scripts.GetVersion(ctx, sc.ID, version)
	if err != nil || v == nil {
		return "", refused("%s has no version %d", sc.Name, version)
	}
	r := &Request{
		ID: NewID(), SubjectKind: SubjectScriptVersion, Purpose: PurposeCallHandler, ScriptID: sc.ID, ScriptName: sc.Name,
		Version: version, SourceSHA256: SourceHash(v.Source), RequesterEmail: actor.Email, RequesterPersona: actor.Persona,
		Reason: reason,
	}
	if r.Changes, err = s.changesSince(ctx, sc.ID, PurposeCallHandler, v); err != nil {
		return "", err
	}
	err = s.Store.Insert(ctx, r)
	if !errors.Is(err, ErrPending) {
		return r.ID, err //nolint:wrapcheck // read by identity
	}
	pending, err := s.Store.List(ctx, Filter{Status: StatusPending, ScriptID: sc.ID, Purpose: PurposeCallHandler})
	if err != nil {
		return "", err //nolint:wrapcheck // read by identity
	}
	for _, p := range pending {
		if p.Version == version {
			return p.ID, nil
		}
	}
	return "", ErrNotFound
}
