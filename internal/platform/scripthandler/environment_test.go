package scripthandler

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appenv"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
)

// envs answers the environments of the one application holding s1.
type envs struct{ err error }

func (e envs) ForScript(_ context.Context, scriptID, env string) (*appenv.Script, error) {
	if e.err != nil {
		return nil, e.err
	}
	if scriptID != "s1" {
		return &appenv.Script{}, nil
	}
	if env != "dev" && env != "prod" {
		return nil, appenv.ErrNoEnvironment
	}
	return &appenv.Script{OK: true, Environment: env}, nil
}

func (e envs) EnvironmentNames(_ context.Context, scriptID string) ([]string, error) {
	if e.err != nil {
		return nil, e.err
	}
	if scriptID != "s1" {
		return []string{}, nil
	}
	return []string{"dev", "prod"}, nil
}

// webhookEnvs is the world's webhooks, able to set a handler's environment.
type webhookEnvs struct{ *webhooks }

func (h webhookEnvs) SetHandlerEnvironment(_ context.Context, id, env, _ string) error {
	x := h.byID[id]
	x.Environment = env
	h.byID[id] = x
	return nil
}

// A schedule or webhook handler of an application's script names the
// environment its runs execute in (#2696).
func TestHandlerEnvironment(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	app := w.add("s1", "daily", "jane@example.com", mainSource)
	loose := w.add("s2", "loose", "jane@example.com", mainSource)
	svc := w.service()
	svc.Environments = envs{}
	svc.Webhooks = webhookEnvs{w.webhooks}

	out, err := svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindSchedule, Cron: "@daily", Environment: "dev"})
	require.NoError(t, err)
	assert.Equal(t, "dev", out[fieldEnvironment])
	id, _ := out[fieldHandler].(string)
	out, err = svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindSchedule, Handler: id, Cron: "@hourly"})
	require.NoError(t, err)
	assert.Equal(t, "dev", out[fieldEnvironment], "a change that names none keeps the handler's")

	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindSchedule, Cron: "@weekly", Environment: "qa"})
	require.ErrorIs(t, err, ErrInvalid)
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: loose, Kind: KindSchedule, Cron: "@weekly", Environment: "dev"})
	require.ErrorContains(t, err, "belongs to no application")

	wh, err := svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindWebhook, EventSource: "shop", Environment: "dev"})
	require.NoError(t, err)
	assert.Equal(t, "dev", wh[fieldEnvironment])
	whID, _ := wh[fieldHandler].(string)
	wh, err = svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindWebhook, EventSource: "shop", Handler: whID, Environment: "prod"})
	require.NoError(t, err)
	assert.Equal(t, "prod", wh[fieldEnvironment])

	names, err := svc.EnvironmentNames(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, []string{"dev", "prod"}, names)

	svc.Environments = envs{err: errors.New("down")}
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindSchedule, Cron: "@weekly", Environment: "dev"})
	require.Error(t, err)
	_, err = svc.EnvironmentNames(ctx, "s1")
	require.Error(t, err)

	svc.Environments = nil
	names, err = svc.EnvironmentNames(ctx, "s1")
	require.NoError(t, err)
	assert.Empty(t, names)
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindSchedule, Cron: "@weekly", Environment: "dev"})
	require.ErrorIs(t, err, ErrInvalid)

	svc.Environments = envs{}
	svc.Webhooks = w.webhooks
	_, err = svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindWebhook, EventSource: "orders", Environment: "dev"})
	require.ErrorContains(t, err, "run in no environment")
}

// handler_environment points an existing schedule or webhook handler at an
// environment (#2696).
func TestSetEnvironment(t *testing.T) {
	ctx := context.Background()
	w := newWorld()
	app := w.add("s1", "daily", "jane@example.com", mainSource)
	svc := w.service()
	svc.Environments = envs{}
	svc.Webhooks = webhookEnvs{w.webhooks}
	sched, err := svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindSchedule, Cron: "@daily"})
	require.NoError(t, err)
	id, _ := sched[fieldHandler].(string)
	out, err := svc.SetEnvironment(ctx, id, "dev", admin)
	require.NoError(t, err)
	assert.Equal(t, "dev", out[fieldEnvironment])
	_, err = svc.SetEnvironment(ctx, id, "", admin)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = svc.SetEnvironment(ctx, id, "qa", admin)
	require.ErrorIs(t, err, ErrInvalid)
	_, err = svc.SetEnvironment(ctx, "nope", "dev", admin)
	require.Error(t, err)

	wh, err := svc.Set(ctx, SetRequest{Caller: admin, Script: app, Kind: KindWebhook, EventSource: "shop"})
	require.NoError(t, err)
	whID, _ := wh[fieldHandler].(string)
	out, err = svc.SetEnvironment(ctx, whID, "prod", admin)
	require.NoError(t, err)
	assert.Equal(t, "prod", out[fieldEnvironment])

	ep, err := svc.Set(ctx, SetRequest{
		Caller: admin, Script: w.add("s3", "ep", "jane@example.com", endpointSource), Kind: KindEndpoint,
		Endpoint: epcmd.Input{Endpoint: "daily-ep", AuthMode: epbinding.AuthAuthenticated},
	})
	require.NoError(t, err)
	epID, _ := ep[fieldHandler].(string)
	svc.Environments = everyScript{}
	_, err = svc.SetEnvironment(ctx, epID, "dev", admin)
	require.ErrorContains(t, err, "only a schedule or webhook handler")
}

// everyScript holds every script in an application with dev.
type everyScript struct{}

func (everyScript) ForScript(context.Context, string, string) (*appenv.Script, error) {
	return &appenv.Script{OK: true, Environment: "dev"}, nil
}
