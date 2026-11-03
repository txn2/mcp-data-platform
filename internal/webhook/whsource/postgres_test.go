package whsource

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func outboxSource() Source {
	return Source{
		Name: "orders-outbox", Connection: "scratch",
		Config: Config{Input: InputPostgres, Postgres: &Postgres{Connection: "orders", Mode: PostgresOutbox, Table: "public.outbox"}},
	}
}

func notifySource() Source {
	s := outboxSource()
	s.Config.Postgres = &Postgres{Connection: "orders", Mode: PostgresNotify, Channels: []string{"order_events"}}
	return s
}

func TestPostgres_Defaults(t *testing.T) {
	s := outboxSource().WithDefaults()
	assert.True(t, s.IsPostgres())
	assert.False(t, s.IsIMAP())
	assert.Equal(t, DefaultPostgresPollSeconds, s.Config.Postgres.PollSeconds)
	assert.Equal(t, DefaultPostgresMaxPayloadBytes, s.Config.Postgres.MaxPayloadBytes)
	require.NoError(t, Validate(s))
	require.NoError(t, Validate(notifySource().WithDefaults()))
	assert.False(t, Source{}.WithDefaults().IsPostgres())
}

func TestPostgres_Refusals(t *testing.T) {
	long := "public." + string(make([]byte, 60))
	cases := map[string]struct {
		base   func() Source
		mutate func(s *Source)
		want   string
	}{
		"no block":        {outboxSource, func(s *Source) { s.Config.Postgres = nil }, "config.postgres is required"},
		"auth":            {outboxSource, func(s *Source) { s.Auth.Secret = "pw" }, "takes no auth"},
		"auth mode":       {outboxSource, func(s *Source) { s.Auth.Mode = AuthHMAC }, "takes no auth"},
		"connection":      {outboxSource, func(s *Source) { s.Config.Postgres.Connection = " " }, "connection is required"},
		"poll":            {outboxSource, func(s *Source) { s.Config.Postgres.PollSeconds = 4000 }, "poll_seconds must be between"},
		"payload":         {outboxSource, func(s *Source) { s.Config.Postgres.MaxPayloadBytes = 2 << 20 }, "max_payload_bytes must be between"},
		"mode":            {outboxSource, func(s *Source) { s.Config.Postgres.Mode = "poll" }, "mode must be notify or outbox"},
		"bare table":      {outboxSource, func(s *Source) { s.Config.Postgres.Table = "outbox" }, "schema.table"},
		"upper table":     {outboxSource, func(s *Source) { s.Config.Postgres.Table = "public.Outbox" }, "schema.table"},
		"long table":      {outboxSource, func(s *Source) { s.Config.Postgres.Table = long }, "schema.table"},
		"outbox channels": {outboxSource, func(s *Source) { s.Config.Postgres.Channels = []string{"x"} }, "channels is for mode notify"},
		"notify table":    {notifySource, func(s *Source) { s.Config.Postgres.Table = "public.outbox" }, "table is for mode outbox"},
		"no channels":     {notifySource, func(s *Source) { s.Config.Postgres.Channels = nil }, "between 1 and 20 channels"},
		"empty channel":   {notifySource, func(s *Source) { s.Config.Postgres.Channels = []string{" "} }, "empty or longer"},
		"block on http":   {notifySource, func(s *Source) { s.Config.Input = InputHTTP; s.Auth = Auth{Mode: AuthHMAC, Secret: "s"} }, "only for a source whose input is postgres"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			s := tc.base()
			tc.mutate(&s)
			err := Validate(s.WithDefaults())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
	s := outboxSource()
	s.Config.Input, s.Config.Postgres = "kafka", nil
	require.ErrorContains(t, Validate(s.WithDefaults()), "config.input must be http, imap or postgres")
}
