package scriptexec

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appenv"
)

type envPins map[string]int

func (p envPins) ForScript(_ context.Context, _, env string) (*appenv.Script, error) {
	v, ok := p[env]
	if !ok {
		return nil, appenv.ErrNoEnvironment
	}
	return &appenv.Script{OK: true, Environment: env, Version: v, Variables: map[string]string{"A": env}}, nil
}

// A schedule naming an environment fires that environment's pinned version
// and records the environment on the run (#2696).
func TestScheduler_AFireRunsInTheSchedulesEnvironment(t *testing.T) {
	fire := time.Date(2026, 8, 14, 7, 0, 0, 0, time.UTC)
	s, store, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
	store.schedules[0].Environment = "dev"
	s.cfg.environments = envPins{"dev": 3}
	_ = s.pass(context.Background())
	runs := store.materialized()
	require.Len(t, runs, 1)
	assert.Equal(t, "dev", runs[0].Environment)
	assert.Equal(t, 3, runs[0].Version)

	s2, store2, _ := schedulerOver(t, fire, fire.Add(time.Minute), nil)
	store2.schedules[0].Environment = "qa"
	s2.cfg.environments = envPins{}
	_ = s2.pass(context.Background())
	assert.Empty(t, store2.materialized(), "an environment the application lacks fires nothing")
}
