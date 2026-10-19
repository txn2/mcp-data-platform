package scripttoken

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var now = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

func TestSignVerifyRoundTrip(t *testing.T) {
	key := Key("s3cret")
	at := Binding{Endpoint: "credit"}
	tok, err := Sign(key, at, Mint{
		Claims: map[string]any{"applicant": "a-17", "amount": 1200.5, "tags": []any{"x"}},
		TTL:    time.Hour, SingleUse: true,
	}, now)
	require.NoError(t, err)

	got, err := Verify(key, at, tok, now.Add(59*time.Minute))
	require.NoError(t, err)
	assert.Equal(t, map[string]any{"applicant": "a-17", "amount": 1200.5, "tags": []any{"x"}}, got.Claims)
	assert.Equal(t, now.Add(time.Hour), got.Expires)
	assert.True(t, got.SingleUse)
	assert.Len(t, got.ID, 2*idBytes)
}

func TestVerifyRefusals(t *testing.T) {
	key := Key("s3cret")
	at := Binding{Endpoint: "credit"}
	tok, err := Sign(key, at, Mint{Claims: map[string]any{"n": 1.0}, TTL: time.Minute}, now)
	require.NoError(t, err)
	enc, sig, _ := strings.Cut(tok, ".")
	other := base64.RawURLEncoding.EncodeToString([]byte(`{"c":{"n":2},"b":["endpoint:credit"],"x":9999999999,"i":"z"}`))

	cases := map[string]struct {
		key   []byte
		at    Binding
		token string
		when  time.Time
		want  string
	}{
		"expired":             {key, at, tok, now.Add(time.Minute), ReasonExpired},
		"claims changed":      {key, at, other + "." + sig, now, ReasonTampered},
		"another key":         {Key("other"), at, tok, now, ReasonTampered},
		"another endpoint":    {key, Binding{Endpoint: "other"}, tok, now, ReasonOtherEndpoint},
		"outside an endpoint": {key, Binding{ScriptID: "s1"}, tok, now, ReasonOtherEndpoint},
		"no signature":        {key, at, enc, now, ReasonMalformed},
		"empty":               {key, at, "", now, ReasonMalformed},
		"too long":            {key, at, strings.Repeat("a", MaxTokenLen+1), now, ReasonMalformed},
		"signature not b64":   {key, at, enc + ".!!", now, ReasonMalformed},
		"body not b64":        {key, at, "!!." + base64.RawURLEncoding.EncodeToString(mac(key, "!!")), now, ReasonMalformed},
		"body not json": {
			key, at, "bm90LWpzb24." + base64.RawURLEncoding.EncodeToString(mac(key, "bm90LWpzb24")),
			now, ReasonMalformed,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Verify(tc.key, tc.at, tc.token, tc.when)
			require.ErrorIs(t, err, ErrRefused)
			assert.Equal(t, tc.want, Reason(err))
			assert.NotEmpty(t, err.Error())
		})
	}
	assert.Empty(t, Reason(errors.New("other")))
}

// A token minted outside an endpoint request is bound to its script; one
// naming endpoints verifies at each of them and nowhere else.
func TestBindings(t *testing.T) {
	key := Key("k")
	script := Binding{ScriptID: "s1"}
	tok, err := Sign(key, script, Mint{TTL: time.Minute}, now)
	require.NoError(t, err)
	got, err := Verify(key, script, tok, now)
	require.NoError(t, err)
	assert.Equal(t, map[string]any{}, got.Claims, "no claims reads back as an empty dict, never null")
	_, err = Verify(key, Binding{ScriptID: "s2"}, tok, now)
	assert.Equal(t, ReasonOtherEndpoint, Reason(err))

	named, err := Sign(key, script, Mint{TTL: time.Minute, Endpoints: []string{"apply", "submit"}}, now)
	require.NoError(t, err)
	for _, e := range []string{"apply", "submit"} {
		_, err := Verify(key, Binding{Endpoint: e}, named, now)
		require.NoError(t, err)
	}
	_, err = Verify(key, script, named, now)
	assert.Equal(t, ReasonOtherEndpoint, Reason(err), "naming endpoints drops the minting script")
}

func TestSignRefusesWhatCannotBeAToken(t *testing.T) {
	key := Key("k")
	at := Binding{Endpoint: "e"}
	for name, m := range map[string]Mint{
		"no ttl":         {TTL: 0},
		"too long ttl":   {TTL: MaxTTL + time.Second},
		"empty endpoint": {TTL: time.Minute, Endpoints: []string{" "}},
		"many endpoints": {TTL: time.Minute, Endpoints: make([]string, MaxEndpoints+1)},
		"big claims":     {TTL: time.Minute, Claims: map[string]any{"x": strings.Repeat("a", MaxClaimsBytes)}},
		"unencodable":    {TTL: time.Minute, Claims: map[string]any{"f": func() {}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Sign(key, at, m, now)
			require.Error(t, err)
		})
	}
}

func TestPostgresUses(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	uses := NewUses(db)
	ctx := context.Background()
	exp := now.Add(time.Hour)

	mock.ExpectQuery("INSERT INTO script_token_uses").WithArgs("t1", exp).
		WillReturnRows(sqlmock.NewRows([]string{"token_id"}).AddRow("t1"))
	first, err := uses.Use(ctx, "t1", exp)
	require.NoError(t, err)
	assert.True(t, first)

	mock.ExpectQuery("INSERT INTO script_token_uses").WithArgs("t1", exp).
		WillReturnRows(sqlmock.NewRows([]string{"token_id"}))
	again, err := uses.Use(ctx, "t1", exp)
	require.NoError(t, err)
	assert.False(t, again)

	mock.ExpectQuery("INSERT INTO script_token_uses").WillReturnError(errors.New("down"))
	_, err = uses.Use(ctx, "t2", exp)
	require.Error(t, err)

	mock.ExpectExec("DELETE FROM script_token_uses").WithArgs(now).WillReturnResult(sqlmock.NewResult(0, 3))
	n, err := uses.Sweep(ctx, now)
	require.NoError(t, err)
	assert.Equal(t, int64(3), n)

	mock.ExpectExec("DELETE FROM script_token_uses").WillReturnError(errors.New("down"))
	_, err = uses.Sweep(ctx, now)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
