package eppreview

import (
	"context"
	"fmt"
	"time"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epform"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Versions reads a script's saved versions.
type Versions interface {
	GetVersion(ctx context.Context, scriptID string, version int) (*script.Version, error)
}

// Previews opens previews.
type Previews struct {
	Store    *Store
	Versions Versions
	// BaseURL is the deployment's public address, empty for a relative link.
	BaseURL string
}

// Opened is a preview just opened: its id and the address its page is at.
type Opened struct {
	ID        string    `json:"id"`
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Open opens a preview of sc for o: the unsaved source when source is not
// empty, else the saved version (the latest for 0). Only the script's owner
// or an administrator opens one, and only of a form.
func (p *Previews) Open(ctx context.Context, o Opener, sc *script.Script, version int, source string) (*Opened, error) {
	if o.Email == "" || o.UserID == "" {
		return nil, fmt.Errorf("a preview is opened by a signed-in person with an email: %w", ErrNotAllowed)
	}
	if !o.Admin && !sc.OwnedBy(o.Email) {
		return nil, fmt.Errorf("the script %s is not yours: its owner or an administrator previews its form: %w", sc.Name, ErrNotAllowed)
	}
	r := &Record{ScriptID: sc.ID, CreatedBy: o.Email, UserID: o.UserID, Roles: o.Roles, AuthType: o.AuthType, Source: source}
	checked, err := p.rendered(ctx, sc, version, r)
	if err != nil {
		return nil, err
	}
	if !epform.DefinesSource(checked) {
		return nil, &RefusedError{Msg: "this source is not a form: a form defines form_spec(req) and form_submit(req, values)"}
	}
	r.ID = newID()
	r.ExpiresAt = p.Store.now().Add(TTL)
	if err := p.Store.Insert(ctx, r); err != nil {
		return nil, err
	}
	return &Opened{ID: r.ID, URL: p.BaseURL + PagePrefix + r.ID, ExpiresAt: r.ExpiresAt}, nil
}

// rendered is the source a preview renders: the unsaved source r carries, or
// the saved version (the latest for 0), which r then names.
func (p *Previews) rendered(ctx context.Context, sc *script.Script, version int, r *Record) (string, error) {
	if r.Source != "" {
		return r.Source, nil
	}
	if version <= 0 {
		version = sc.Version
	}
	v, err := p.Versions.GetVersion(ctx, sc.ID, version)
	if err != nil || v == nil {
		return "", &RefusedError{Msg: fmt.Sprintf("%s has no version %d", sc.Name, version)}
	}
	r.Version = v.Version
	return v.Source, nil
}

// RefusedError is a preview refused for a reason the caller can act on.
type RefusedError struct{ Msg string }

func (e *RefusedError) Error() string { return e.Msg }
