// Package epsource makes the webhook source a form stores its submissions in
// (#2178), when the form is bound to an endpoint: named for the endpoint, on
// the Trino connection the binding names or the only one there is, with the
// personas a script may decrypt the form's sensitive fields as; and finds the
// one made for a form script before.
package epsource

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/namesort"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// maxSourceName is the longest a source name may be.
const maxSourceName = 63

// SourceAdmin is the webhook source service as a form's source is made and
// changed through it (whadmin.Service).
type SourceAdmin interface {
	List(ctx context.Context) ([]whsource.Source, error)
	CreateForm(ctx context.Context, src whsource.Source) (whsource.Source, error)
	SetDecryptPersonas(ctx context.Context, name string, personas []string) (whsource.Source, error)
}

// SourceRequest is a form bound to an endpoint, asking for its source.
type SourceRequest struct {
	ScriptID string
	Endpoint string
	// Connection is the Trino connection the source's tables go on; empty
	// is the deployment's only connection with a scratch catalog.
	Connection string
	// DecryptPersonas replaces the personas the form's sensitive fields may
	// be decrypted as; nil keeps them.
	DecryptPersonas []string
	Actor           string
}

// SourceResult is the source a form stores into.
type SourceResult struct {
	Source          string   `json:"source"`
	Table           string   `json:"table"`
	Connection      string   `json:"connection"`
	DecryptPersonas []string `json:"decrypt_personas"`
	Created         bool     `json:"created"`
}

// Maker makes the webhook source a form stores its submissions in, when the
// form is bound to an endpoint (#2178).
type Maker struct {
	Admin SourceAdmin
	// Connections names the connections a source's tables can be created
	// on (trino.Toolkit.ScratchConnections).
	Connections func() []string
}

// Ensure is the form's source: the one made for its script before, with its
// decrypt personas replaced when the request names them, or a new one on the
// connection the request names, or on the only one there is.
func (m *Maker) Ensure(ctx context.Context, req SourceRequest) (SourceResult, error) {
	list, err := m.Admin.List(ctx)
	if err != nil {
		return SourceResult{}, fmt.Errorf("listing webhook sources: %w", err)
	}
	for _, s := range list {
		if s.IsForm() && s.Config.Form != nil && s.Config.Form.ScriptID == req.ScriptID {
			return m.existing(ctx, s, req)
		}
	}
	return m.create(ctx, req)
}

// Find is the source made for a form script before, and whether there is
// one; it makes nothing.
func (m *Maker) Find(ctx context.Context, scriptID string) (SourceResult, bool, error) {
	list, err := m.Admin.List(ctx)
	if err != nil {
		return SourceResult{}, false, fmt.Errorf("listing webhook sources: %w", err)
	}
	for _, s := range list {
		if s.IsForm() && s.Config.Form != nil && s.Config.Form.ScriptID == scriptID {
			return resultOf(s, false), true, nil
		}
	}
	return SourceResult{}, false, nil
}

// ConnectionChoices names the connections a form's source can be made on.
func (m *Maker) ConnectionChoices() []string {
	if m.Connections == nil {
		return []string{}
	}
	// In name order, the order the form's picker lists them in (#2389).
	out := append([]string{}, m.Connections()...)
	slices.SortStableFunc(out, namesort.Compare)
	return out
}

func (m *Maker) existing(ctx context.Context, src whsource.Source, req SourceRequest) (SourceResult, error) {
	if req.Connection != "" && req.Connection != src.Connection {
		return SourceResult{}, refusal("this form's submissions are stored on connection %s, in %s; "+
			"a source's connection does not change", src.Connection, src.TableName())
	}
	if req.DecryptPersonas != nil {
		updated, err := m.Admin.SetDecryptPersonas(ctx, src.Name, req.DecryptPersonas)
		if err != nil {
			return SourceResult{}, err //nolint:wrapcheck // the refusal names the persona
		}
		src = updated
	}
	return resultOf(src, false), nil
}

func (m *Maker) create(ctx context.Context, req SourceRequest) (SourceResult, error) {
	conn, err := m.connection(req.Connection)
	if err != nil {
		return SourceResult{}, err
	}
	name := whsource.FormSourceName(req.Endpoint)
	if len(name) > maxSourceName {
		return SourceResult{}, refusal("a form's source is named %s, longer than %d characters; "+
			"bind the form at an endpoint name of at most %d characters", name, maxSourceName, maxSourceName-len(whsource.FormPrefix))
	}
	src, err := m.Admin.CreateForm(ctx, whsource.Source{
		Name: name, Enabled: true, Connection: conn, CreatedBy: req.Actor,
		Config: whsource.Config{
			Input: whsource.InputForm,
			Form:  &whsource.Form{ScriptID: req.ScriptID, Endpoint: req.Endpoint, DecryptPersonas: req.DecryptPersonas},
		},
	})
	if errors.Is(err, whsource.ErrExists) {
		return SourceResult{}, refusal("a webhook source named %s already exists and is not this form's; "+
			"bind the form at another endpoint name", name)
	}
	if err != nil {
		return SourceResult{}, fmt.Errorf("making the form's webhook source: %w", err)
	}
	return resultOf(src, true), nil
}

// connection is the connection named, or the only one a source can be made
// on.
func (m *Maker) connection(named string) (string, error) {
	if named != "" {
		return named, nil
	}
	var list []string
	if m.Connections != nil {
		list = m.Connections()
	}
	switch len(list) {
	case 0:
		return "", refusal("a form stores its submissions in a webhook source, whose table needs a Trino " +
			"connection with a scratch catalog that accepts writes, and this deployment has none")
	case 1:
		return list[0], nil
	}
	return "", refusal("name the connection the form's submissions are stored on: one of %s", strings.Join(list, ", "))
}

func resultOf(s whsource.Source, created bool) SourceResult {
	personas := []string{}
	if s.Config.Form != nil && s.Config.Form.DecryptPersonas != nil {
		personas = s.Config.Form.DecryptPersonas
	}
	return SourceResult{
		Source: s.Name, Table: s.TableName(), Connection: s.Connection, DecryptPersonas: personas, Created: created,
	}
}

// refusal is a setting the binding has to change, as whsource refuses one:
// ErrInvalid, rendered as written.
func refusal(format string, args ...any) error {
	return whsource.Refusal(format, args...) //nolint:wrapcheck // ErrInvalid is what a surface renders
}
