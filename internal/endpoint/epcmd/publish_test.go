package epcmd

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/approval"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/eppreview"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// approvals is an Approvals with one approved version per script.
type approvals struct {
	approved map[string]int
	recorded []int
	err      error
}

func (a *approvals) Approved(_ context.Context, scriptID, _ string) int { return a.approved[scriptID] }

func (a *approvals) RecordSelfApproval(_ context.Context, actor approval.Actor, sc *script.Script, version int) error {
	if a.err != nil {
		return a.err
	}
	if !actor.Admin {
		return approval.ErrNotAllowed
	}
	a.recorded = append(a.recorded, version)
	a.approved[sc.ID] = version
	return nil
}

// owned is the contact form, owned by jane.
func owned() *script.Script {
	c := *contact
	c.OwnerEmail, c.Version = "jane@example.com", 2
	return &c
}

func janeReq(cmd string, in Input) Request {
	return Request{Command: cmd, Input: in, Script: owned(), Actor: "jane@example.com"}
}

// TestOwnerPublishesAnInternalForm is #2494's fourth requirement: the owner
// of a form binds, enables and disables it authenticated with no
// administrator; another person's script, and an endpoint that is not a
// form, are refused naming why.
func TestOwnerPublishesAnInternalForm(t *testing.T) {
	s := newStore()
	h := handler(s)
	h.Approvals = &approvals{approved: map[string]int{}}
	ctx := context.Background()

	out, err := h.Run(ctx, janeReq(CmdSet, Input{Endpoint: "contact", AuthMode: epbinding.AuthAuthenticated, Callers: []string{"*"}, Methods: []string{"GET", "POST"}}))
	require.NoError(t, err)
	assert.Equal(t, "https://data.example.com/endpoints/contact", out["url"])
	_, err = h.Run(ctx, janeReq(CmdDisable, Input{}))
	require.NoError(t, err)
	_, err = h.Run(ctx, janeReq(CmdEnable, Input{}))
	require.NoError(t, err)

	other := janeReq(CmdSet, Input{Endpoint: "x", AuthMode: epbinding.AuthAuthenticated})
	other.Actor = "bob@example.com"
	_, err = h.Run(ctx, other)
	require.ErrorIs(t, err, epbinding.ErrNotAllowed)
	assert.Contains(t, err.Error(), "the script contact is not yours")

	notForm := janeReq(CmdSet, Input{Endpoint: "x", AuthMode: epbinding.AuthAuthenticated})
	notForm.Script = &script.Script{ID: "s1", Name: "lookup", OwnerEmail: "jane@example.com", Source: lookup.Source}
	_, err = h.Run(ctx, notForm)
	require.ErrorIs(t, err, epbinding.ErrNotAllowed)
	assert.Contains(t, err.Error(), "an administrator binds an endpoint that is not a form")

	none := janeReq(CmdSet, Input{})
	none.Script = nil
	_, err = h.Run(ctx, none)
	require.ErrorIs(t, err, epbinding.ErrNotAllowed)
}

// TestPublicNeedsAnApprovedVersion is #2494's fifth requirement: the owner
// is refused public and bot_check until a version is approved, naming the
// request to file; once one is, they switch it whenever they choose. An
// administrator's binding approves the version served, self-approved.
func TestPublicNeedsAnApprovedVersion(t *testing.T) {
	s := newStore()
	h := handler(s)
	a := &approvals{approved: map[string]int{}}
	h.Approvals = a
	ctx := context.Background()

	for _, in := range []Input{
		{Endpoint: "contact", AuthMode: epbinding.AuthPublic, Methods: []string{"GET", "POST"}},
		{Endpoint: "contact", AuthMode: epbinding.AuthBotCheck, BotCheck: "default", Methods: []string{"GET", "POST"}},
	} {
		mode := in.AuthMode
		_, err := h.Run(ctx, janeReq(CmdSet, in))
		require.ErrorIs(t, err, epbinding.ErrNotAllowed, mode)
		assert.Contains(t, err.Error(), "manage_approval action=submit script=s9 version=2", mode)
		assert.Contains(t, err.Error(), "Request public access", mode)
	}
	assert.Empty(t, s.byScript)

	a.approved["s9"] = 2
	_, err := h.Run(ctx, janeReq(CmdSet, Input{Endpoint: "contact", AuthMode: epbinding.AuthPublic, Methods: []string{"GET", "POST"}}))
	require.NoError(t, err, "an approved version lets the owner go public")

	// An administrator binding a form with no approved version approves the
	// version it serves.
	s2 := newStore()
	h2 := handler(s2)
	a2 := &approvals{approved: map[string]int{}}
	h2.Approvals = a2
	req := setReq(Input{Endpoint: "contact", AuthMode: epbinding.AuthPublic, Methods: []string{"GET", "POST"}})
	req.Script = owned()
	_, err = h2.Run(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, []int{2}, a2.recorded)
	_, err = h2.Run(ctx, req)
	require.NoError(t, err)
	assert.Equal(t, []int{2}, a2.recorded, "an approved version is not approved again")

	a2.approved = map[string]int{}
	a2.err = errors.New("down")
	_, err = h2.Run(ctx, req)
	require.ErrorIs(t, err, ErrStore)

	// No queue: an administrator binds as before, the owner cannot go public.
	h3 := handler(newStore())
	_, err = h3.Run(ctx, req)
	require.NoError(t, err)
	_, err = h3.Run(ctx, janeReq(CmdSet, Input{Endpoint: "contact", AuthMode: epbinding.AuthPublic}))
	require.ErrorIs(t, err, epbinding.ErrNotAllowed)
}

func TestOwnerEnablingAPublicFormNeedsAnApprovedVersion(t *testing.T) {
	s := newStore()
	s.byScript["s9"] = epbinding.Binding{ScriptID: "s9", Name: "contact", AuthMode: epbinding.AuthPublic, Methods: []string{"GET"}}
	h := handler(s)
	h.Approvals = &approvals{approved: map[string]int{}}
	_, err := h.Run(context.Background(), janeReq(CmdEnable, Input{}))
	require.ErrorIs(t, err, epbinding.ErrNotAllowed)
}

// TestFormPreviewCommand is manage_script form_preview: the link the agent
// hands back, for the caller, of a saved version or the unsaved source.
func TestFormPreviewCommand(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	h := handler(newStore())
	ctx := context.Background()
	req := janeReq(CmdFormPreview, Input{PreviewVersion: 1})
	req.Opener = eppreview.Opener{UserID: "u-jane", Roles: []string{"analyst"}}

	_, err = h.Run(ctx, req)
	require.ErrorContains(t, err, "cannot preview a form")

	h.Previews = &eppreview.Previews{Store: eppreview.NewStore(db), Versions: versionsOf{1: contact.Source}, BaseURL: "https://data.example.com"}
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM form_previews")).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO form_previews")).
		WithArgs(sqlmock.AnyArg(), "s9", 1, "", "jane@example.com", "u-jane", []byte(`["analyst"]`), "", sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	out, err := h.Run(ctx, req)
	require.NoError(t, err)
	assert.Contains(t, out["preview_url"], "https://data.example.com/forms/_preview/fp")
	assert.Contains(t, out["message"], "version 1 of contact's form")

	req.Source, req.Input.PreviewVersion = contact.Source, 0
	mock.ExpectExec("DELETE").WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("INSERT").WillReturnRows(sqlmock.NewRows([]string{"created_at"}).AddRow(time.Now()))
	out, err = h.Run(ctx, req)
	require.NoError(t, err)
	assert.Contains(t, out["message"], "the unsaved source")

	req.Source, req.Input.PreviewVersion = "", 1
	mock.ExpectExec("DELETE").WillReturnError(sql.ErrConnDone)
	_, err = h.Run(ctx, req)
	require.Error(t, err)
	none := req
	none.Script = nil
	_, err = h.Run(ctx, none)
	require.ErrorContains(t, err, "names the script")
	require.NoError(t, mock.ExpectationsWereMet())
	assert.False(t, Writes(CmdFormPreview))
}

type versionsOf map[int]string

func (v versionsOf) GetVersion(_ context.Context, _ string, n int) (*script.Version, error) {
	src, ok := v[n]
	if !ok {
		return nil, errors.New("no such version")
	}
	return &script.Version{Version: n, Source: src}, nil
}
