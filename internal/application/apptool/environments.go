package apptool

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
)

func (t *Tool) promote(ctx context.Context, actor application.Actor, in Input) (any, error) {
	return t.Service.Promote(ctx, actor, in.Application, in.Environment) //nolint:wrapcheck // the caller's message
}

func (t *Tool) addEnvironment(ctx context.Context, actor application.Actor, in Input) (any, error) {
	e := application.Environment{Name: in.Environment, Schema: in.Schema, RequiresApproval: in.RequiresApproval != nil && *in.RequiresApproval}
	return t.Service.AddEnvironment(ctx, actor, in.Application, e, in.After) //nolint:wrapcheck // the caller's message
}

func (t *Tool) removeEnvironment(ctx context.Context, actor application.Actor, in Input) (any, error) {
	return t.Service.RemoveEnvironment(ctx, actor, in.Application, in.Environment) //nolint:wrapcheck // the caller's message
}

func (t *Tool) setEnvironment(ctx context.Context, actor application.Actor, in Input) (any, error) {
	if in.RequiresApproval == nil {
		return nil, &appsvc.RefusedError{Msg: "set_environment: name requires_approval (true or false)"}
	}
	return t.Service.SetEnvironmentApproval(ctx, actor, in.Application, in.Environment, *in.RequiresApproval) //nolint:wrapcheck // the caller's message
}

func (t *Tool) variables(ctx context.Context, actor application.Actor, in Input) (any, error) {
	vars, err := t.Service.Variables(ctx, actor, in.Application)
	if err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	return map[string]any{keyApplication: in.Application, "variables": vars}, nil
}

func (t *Tool) setVariable(ctx context.Context, actor application.Actor, in Input) (any, error) {
	v := application.Variable{Environment: in.Environment, Name: in.Variable, Value: in.Value}
	if err := t.Service.SetVariable(ctx, actor, in.Application, v); err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	return map[string]any{keyApplication: in.Application, keyEnvironment: in.Environment, keyVariable: in.Variable, "set": true}, nil
}

func (t *Tool) deleteVariable(ctx context.Context, actor application.Actor, in Input) (any, error) {
	if err := t.Service.DeleteVariable(ctx, actor, in.Application, in.Environment, in.Variable); err != nil {
		return nil, err //nolint:wrapcheck // the caller's message
	}
	return map[string]any{keyApplication: in.Application, keyEnvironment: in.Environment, keyVariable: in.Variable, "deleted": true}, nil
}
