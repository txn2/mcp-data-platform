package scripthandler

import (
	"context"
	"errors"
	"fmt"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// errNoEndpoints is a deployment that cannot bind a script to an endpoint.
var errNoEndpoints = fmt.Errorf("this deployment cannot bind scripts to endpoints: %w", ErrInvalid)

func endpointRequest(sc *script.Script, c Caller) epcmd.Request {
	return epcmd.Request{Script: sc, Admin: c.Admin, Actor: c.Email, MaxBindings: MaxPerKind}
}

func (s *Service) setEndpoint(ctx context.Context, req SetRequest) (map[string]any, error) {
	if s.Endpoints == nil {
		return nil, errNoEndpoints
	}
	r := endpointRequest(req.Script, req.Caller)
	r.Input, r.TimeoutSeconds, r.Binding = req.Endpoint, req.TimeoutSeconds, req.Handler
	out, err := s.Endpoints.Set(ctx, r)
	if err != nil {
		return nil, err //nolint:wrapcheck // the endpoint kind's refusal is what the caller reads
	}
	return out, nil
}

// binding is the endpoint handler h, read from its own table.
func (s *Service) binding(ctx context.Context, h Handler) (*epbinding.Binding, error) {
	if s.Endpoints == nil {
		return nil, errNoEndpoints
	}
	b, err := s.Endpoints.Store.GetByID(ctx, h.ID)
	if err != nil {
		return nil, storeOr(err)
	}
	return b, nil
}

func (s *Service) enableEndpoint(ctx context.Context, h Handler, sc *script.Script, enabled bool, c Caller) error {
	b, err := s.binding(ctx, h)
	if err != nil {
		return err
	}
	_, err = s.Endpoints.Enable(ctx, endpointRequest(sc, c), b, enabled)
	return err //nolint:wrapcheck // the endpoint kind's refusal is what the caller reads
}

func (s *Service) deleteEndpoint(ctx context.Context, h Handler, sc *script.Script, c Caller) error {
	b, err := s.binding(ctx, h)
	if errors.Is(err, epbinding.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.Endpoints.Delete(ctx, endpointRequest(sc, c), b) //nolint:wrapcheck // the endpoint kind's refusal is what the caller reads
}

func (s *Service) describeEndpoint(ctx context.Context, h Handler, sc *script.Script) (map[string]any, error) {
	b, err := s.binding(ctx, h)
	if err != nil {
		return nil, err
	}
	out, err := s.Endpoints.Describe(ctx, sc, b)
	if err != nil {
		return nil, err //nolint:wrapcheck // the endpoint kind names what it could not read
	}
	return out, nil
}
