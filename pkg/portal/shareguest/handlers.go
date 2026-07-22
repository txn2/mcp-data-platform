package shareguest

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"

	"github.com/google/uuid"
)

// pathKeyToken is the mux path parameter carrying the share's viewer token,
// matching the portal's public route patterns.
const pathKeyToken = "token"

// uniformResponse is the body POST request-link always returns. It is
// identical for a valid email share, a link share with no recipient, a
// revoked or expired share, and an unknown token, so the endpoint confirms
// nothing about any share to a caller who merely holds a URL.
const uniformResponse = "If this share names an email address, a one-time view link has been sent to it."

// logKeyError and logKeyShareID are the structured-logging keys used by the
// silent failure paths.
const (
	logKeyError   = "error"
	logKeyShareID = "share_id"
)

// HandleRequestLink serves POST /portal/view/{token}/request-link: it issues
// a one-time view link to the share's stored recipient address when the share
// qualifies, and answers uniformly either way. The portal registers it on the
// public mux inside the rate limiter and outside the access gate, since its
// whole audience is callers the gate refuses.
func (s *Service) HandleRequestLink(w http.ResponseWriter, r *http.Request) {
	s.tryIssueLink(r.Context(), r.PathValue(pathKeyToken))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": uniformResponse})
}

// issuableShare resolves the share behind token and reports whether it
// qualifies for a one-time link: the flow is configured and the share is
// live, non-public, and names a recipient.
func (s *Service) issuableShare(ctx context.Context, token string) (ShareInfo, bool) {
	if token == "" || !s.linksEnabled() {
		return ShareInfo{}, false
	}
	share, ok := s.resolve(ctx, token)
	if !ok || !share.Live() || share.Public || share.RecipientEmail == "" {
		return ShareInfo{}, false
	}
	return share, true
}

// tryIssueLink mints, stores, and emails a one-time link when the share
// qualifies (see issuableShare) and is under its issue cap. Every other case
// is a silent no-op behind the uniform response; failures are logged, never
// surfaced.
func (s *Service) tryIssueLink(ctx context.Context, token string) {
	share, ok := s.issuableShare(ctx, token)
	if !ok {
		return
	}
	now, since := s.claimWindow()
	count, err := s.links.CountSince(ctx, share.ID, since)
	if err != nil {
		slog.Warn("share guest link: issue-cap query failed", logKeyError, err, logKeyShareID, share.ID)
		return
	}
	if count >= maxLinksPerWindow {
		return
	}
	otk, hash, err := mintOTK()
	if err != nil {
		slog.Warn("share guest link: token generation failed", logKeyError, err, logKeyShareID, share.ID)
		return
	}
	err = s.links.Insert(ctx, Link{
		ID:        uuid.New().String(),
		ShareID:   share.ID,
		TokenHash: hash,
		CreatedAt: now,
		ExpiresAt: now.Add(LinkTTL),
	})
	if err != nil {
		slog.Warn("share guest link: insert failed", logKeyError, err, logKeyShareID, share.ID)
		return
	}
	link := s.baseURL + "/portal/view/" + url.PathEscape(share.Token) + "/guest?otk=" + otk
	if err := s.send(ctx, share.RecipientEmail, link); err != nil {
		slog.Warn("share guest link: send failed", logKeyError, err, logKeyShareID, share.ID)
	}
}

// resubscribeResponse is the body POST resubscribe always returns. Like
// uniformResponse, it is identical for every share state so the endpoint
// confirms nothing to a caller who merely holds a URL.
const resubscribeResponse = "If notification emails to this share's recipient were paused, they have been resumed."

// HandleResubscribe serves POST /portal/view/{token}/resubscribe: it turns
// notification delivery back on for the share's stored recipient address
// (#1022). Opting back in is a deliberate POST-only action, mirroring the
// no-mutation-on-GET rule of the unsubscribe endpoint it reverses. The portal
// registers it beside request-link: rate-limited, outside the access gate.
func (s *Service) HandleResubscribe(w http.ResponseWriter, r *http.Request) {
	s.tryResubscribe(r.Context(), r.PathValue(pathKeyToken))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"message": resubscribeResponse})
}

// tryResubscribe re-enables delivery for the recipient of a live, non-public
// email share. Every other case is a silent no-op behind the uniform
// response; failures are logged, never surfaced.
func (s *Service) tryResubscribe(ctx context.Context, token string) {
	if token == "" || s.resubscribe == nil {
		return
	}
	share, ok := s.resolve(ctx, token)
	if !ok || !share.Live() || share.Public || share.RecipientEmail == "" {
		return
	}
	if err := s.resubscribe(ctx, share.RecipientEmail); err != nil {
		slog.Warn("share guest resubscribe: prefs write failed", logKeyError, err, logKeyShareID, share.ID)
		return
	}
	// The action is unauthenticated by design (its audience is opted-out
	// recipients the gate refuses), so leave an operator-auditable record of
	// every successful preference flip.
	slog.Info("share guest resubscribe: notification delivery resumed", logKeyShareID, share.ID)
}

// HandleClaim serves GET /portal/view/{token}/guest?otk=...: it claims the
// one-time token and, on success, opens a guest session scoped to the share
// and redirects into the viewer. A used, expired, or foreign token redirects
// back to the landing page, which explains and re-offers the request button.
func (s *Service) HandleClaim(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue(pathKeyToken)
	if token == "" {
		http.NotFound(w, r)
		return
	}
	viewPath := "/portal/view/" + url.PathEscape(token)
	invalidPath := viewPath + "?" + linkStatusParam + "=" + linkStatusInvalid
	signed, ok := s.claimGuestSession(r.Context(), token, r.URL.Query().Get("otk"))
	if !ok {
		http.Redirect(w, r, invalidPath, http.StatusSeeOther)
		return
	}
	s.setGuestCookie(w, signed)
	http.Redirect(w, r, viewPath, http.StatusSeeOther)
}

// claimGuestSession atomically consumes the one-time token for the share
// behind the viewer token and returns the signed guest session. ok is false
// when anything disqualifies the claim: unknown share, dead share, missing or
// already-used token, or a token minted for a different share.
func (s *Service) claimGuestSession(ctx context.Context, token, otk string) (string, bool) {
	if otk == "" || !s.linksEnabled() {
		return "", false
	}
	share, ok := s.resolve(ctx, token)
	if !ok || !share.Live() || share.RecipientEmail == "" {
		return "", false
	}
	now := s.now()
	claimed, err := s.links.Claim(ctx, hashOTK(otk), share.ID, now)
	if err != nil {
		slog.Warn("share guest link: claim failed", logKeyError, err, logKeyShareID, share.ID)
		return "", false
	}
	if !claimed {
		return "", false
	}
	signed, err := s.signGuestSession(share.ID, share.RecipientEmail)
	if err != nil {
		slog.Warn("share guest link: session signing failed", logKeyError, err, logKeyShareID, share.ID)
		return "", false
	}
	return signed, true
}
