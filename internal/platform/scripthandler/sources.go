package scripthandler

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// errNoSources is a deployment with no webhook sources.
var errNoSources = fmt.Errorf("this deployment has no webhook sources for a script to run for: %w", ErrInvalid)

// SourceRule is who may run a script for which webhook source
// (sourcerule.Rule).
type SourceRule interface {
	Check(ctx context.Context, source, callerEmail string, admin bool) error
}

// checkSource is the source rule's answer for the caller naming source: its
// refusal as the caller reads it, and a source it could not read as the
// store's failure, so no database text reaches the caller.
func (s *Service) checkSource(ctx context.Context, source string, c Caller) error {
	err := s.Sources.Check(ctx, source, c.Email, c.Admin)
	if err == nil || errors.Is(err, sourcerule.ErrSourceRefused) {
		return err //nolint:wrapcheck // the source rule's refusal is what the caller reads
	}
	return storeErr(err)
}

// SourceLister lists the deployment's webhook sources (whsource.Store, read
// with no decryptor: only names and forms are read).
type SourceLister interface {
	List(ctx context.Context) ([]whsource.Source, error)
}

// SourceNames is the webhook sources the caller may run a script for: every
// one for an administrator, and anyone else the sources made for forms whose
// scripts they own.
func (s *Service) SourceNames(ctx context.Context, c Caller) ([]string, error) {
	out := []string{}
	if s.SourceList == nil {
		return out, nil
	}
	all, err := s.SourceList.List(ctx)
	if err != nil {
		return nil, storeErr(err)
	}
	for _, src := range all {
		if c.Admin {
			out = append(out, src.Name)
			continue
		}
		if !src.IsForm() || src.Config.Form == nil {
			continue
		}
		if sc, err := s.Scripts.GetByID(ctx, src.Config.Form.ScriptID); err == nil && sc != nil && sc.OwnedBy(c.Email) {
			out = append(out, src.Name)
		}
	}
	return out, nil
}
