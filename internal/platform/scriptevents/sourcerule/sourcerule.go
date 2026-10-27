// Package sourcerule is who may run a managed script for which webhook source
// (#2332). A run reads every event its source stores, so naming a source is
// reading it: an administrator may name any source, and anyone else only the
// source made for a form whose script they own.
package sourcerule

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// ErrSourceRefused is a script naming a webhook source its owner may not
// run it for.
var ErrSourceRefused = errors.New("this script cannot run for that webhook source")

// refusal is ErrSourceRefused with the reason the caller is told.
type refusal struct{ reason string }

func (r refusal) Error() string { return r.reason }

func (refusal) Unwrap() error { return ErrSourceRefused }

// SourceReader reads a webhook source by name (whsource.Store).
type SourceReader interface {
	Get(ctx context.Context, name string) (whsource.Source, error)
}

// Rule is who may run a script for which webhook source (#2332). A run
// reads every event the source stores, so naming a source is reading it: an
// administrator may name any source, and anyone else only the source made
// for a form whose script they own, which holds the submissions their own
// form collected.
type Rule struct {
	Sources SourceReader
	// FormOwner is the owner's email of the script a form source was made
	// for.
	FormOwner func(ctx context.Context, scriptID string) (string, error)
}

// NewRule is the rule over the stored webhook sources and the scripts get
// reads, nil without a database. A source is read only for its name and its
// form, so its secrets are not decrypted.
func NewRule(db *sql.DB, get func(ctx context.Context, id string) (*script.Script, error)) *Rule {
	if db == nil {
		return nil
	}
	return &Rule{Sources: whsource.NewStore(db, nil), FormOwner: OwnerOf(get)}
}

// Set sets the webhook source sc runs for to source once the caller may name
// it; a nil source leaves it alone. A nil Rule refuses naming any source.
func (r *Rule) Set(ctx context.Context, sc *script.Script, source *string, callerEmail string, admin bool) error {
	if source == nil || *source == sc.EventSource {
		return nil
	}
	if r == nil && *source != "" {
		return refusal{"this deployment has no webhook sources for a script to run for"}
	}
	if r != nil {
		if err := r.Check(ctx, *source, callerEmail, admin); err != nil {
			return err
		}
	}
	sc.EventSource = *source
	return nil
}

// OwnerOf is Rule.FormOwner over a script read: the owner's email of the
// script with the id, "" for a script that does not exist, which owns
// nothing.
func OwnerOf(get func(ctx context.Context, id string) (*script.Script, error)) func(ctx context.Context, scriptID string) (string, error) {
	return func(ctx context.Context, scriptID string) (string, error) {
		sc, err := get(ctx, scriptID)
		if err != nil {
			return "", fmt.Errorf("reading script %s: %w", scriptID, err)
		}
		if sc == nil {
			return "", nil
		}
		return sc.OwnerEmail, nil
	}
}

// Check reports why a script may not run for source when the caller names it,
// nil when it may. An empty source runs the script for none, which anyone who
// may edit the script may choose.
func (r Rule) Check(ctx context.Context, source, callerEmail string, admin bool) error {
	if source == "" {
		return nil
	}
	src, err := r.read(ctx, source)
	if err != nil || admin {
		return err
	}
	return r.checkOwner(ctx, src, callerEmail)
}

// read is the source named source, or why it cannot be named.
func (r Rule) read(ctx context.Context, source string) (whsource.Source, error) {
	if r.Sources == nil {
		return whsource.Source{}, refusal{"this deployment has no webhook sources"}
	}
	src, err := r.Sources.Get(ctx, source)
	if errors.Is(err, whsource.ErrNotFound) {
		return whsource.Source{}, refusal{fmt.Sprintf("no webhook source is named %q", source)}
	}
	if err != nil {
		return whsource.Source{}, fmt.Errorf("reading webhook source %q: %w", source, err)
	}
	return src, nil
}

// checkOwner admits a caller who is not an administrator only to the source
// of a form whose script they own.
func (r Rule) checkOwner(ctx context.Context, src whsource.Source, callerEmail string) error {
	if !src.IsForm() || src.Config.Form == nil || r.FormOwner == nil {
		return refusal{fmt.Sprintf("only an administrator may run a script for source %q; you may name the source of a form you own", src.Name)}
	}
	owner, err := r.FormOwner(ctx, src.Config.Form.ScriptID)
	if err != nil {
		return fmt.Errorf("reading the form source's script: %w", err)
	}
	if !strings.EqualFold(owner, callerEmail) {
		return refusal{fmt.Sprintf("source %q holds the submissions of a form you do not own", src.Name)}
	}
	return nil
}
