package scripttoken

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.starlark.net/starlark"
	"go.starlark.net/starlarkstruct"
	"go.starlark.net/syntax"
)

// memUses is Uses in memory, the contract the table's primary key keeps.
type memUses struct {
	mu   sync.Mutex
	seen map[string]bool
	err  error
}

func (m *memUses) Use(_ context.Context, id string, _ time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return false, m.err
	}
	if m.seen == nil {
		m.seen = map[string]bool{}
	}
	first := !m.seen[id]
	m.seen[id] = true
	return first, nil
}

func (*memUses) Sweep(context.Context, time.Time) (int64, error) { return 0, nil }

// keys holds "credit-key" for persona "sales" and "open-key" for anyone.
func keys(_ context.Context, name, persona string, admin bool) (string, error) {
	switch {
	case name == "open-key":
		return "open", nil
	case name == "credit-key" && (admin || persona == "sales"):
		return "credit", nil
	case name == "credit-key":
		return "", errors.New(`secret "credit-key" may not be used by persona ` + persona)
	default:
		return "", errors.New("no such secret")
	}
}

func issuer(uses Uses) *Issuer {
	return &Issuer{
		Keys: keys, Uses: uses, Now: func() time.Time { return now },
		Authority: func(roles []string) (string, bool) {
			if len(roles) == 0 {
				return "", false
			}
			return roles[0], roles[0] == "admin"
		},
	}
}

func TestIssuer_MintVerifyAtAnEndpoint(t *testing.T) {
	c := issuer(&memUses{}).Bind(Binding{Endpoint: "credit"}, []string{"sales"})
	ctx := context.Background()
	out, err := c.CallTool(ctx, CapabilityMint, map[string]any{
		"claims": map[string]any{"applicant": "a-1"}, "ttl": 600, "secret": "credit-key", "endpoints": []any{}, "single_use": false,
	})
	require.NoError(t, err)
	tok, _ := out["token"].(string)
	require.NotEmpty(t, tok)

	got, err := c.CallTool(ctx, CapabilityVerify, map[string]any{"token": tok, "secret": "credit-key"})
	require.NoError(t, err)
	assert.Equal(t, true, got["ok"])
	assert.Equal(t, map[string]any{"applicant": "a-1"}, got["claims"])
	assert.Equal(t, now.Add(10*time.Minute).Format(time.RFC3339), got["expires_at"])

	other := issuer(&memUses{}).Bind(Binding{Endpoint: "other"}, []string{"sales"})
	refusedOut, err := other.CallTool(ctx, CapabilityVerify, map[string]any{"token": tok, "secret": "credit-key"})
	require.NoError(t, err, "a refusal is an answer the script reads, not a failure")
	assert.Equal(t, false, refusedOut["ok"])
	assert.Equal(t, ReasonOtherEndpoint, refusedOut["reason"])
}

// A script whose persona the key does not allow cannot mint with it; the
// administrator's persona can.
func TestIssuer_PersonaScoping(t *testing.T) {
	args := map[string]any{"claims": map[string]any{}, "ttl": 60, "secret": "credit-key"}
	_, err := issuer(nil).Bind(Binding{ScriptID: "s"}, []string{"analyst"}).CallTool(context.Background(), CapabilityMint, args)
	require.ErrorContains(t, err, "may not be used by persona analyst")
	_, err = issuer(nil).Bind(Binding{ScriptID: "s"}, []string{"admin"}).CallTool(context.Background(), CapabilityMint, args)
	require.NoError(t, err)
	_, err = (&Issuer{Keys: keys}).Bind(Binding{ScriptID: "s"}, []string{"sales"}).CallTool(context.Background(), CapabilityMint, args)
	require.Error(t, err, "with no authority there is no persona, so a key naming personas is refused")
}

func TestIssuer_SingleUse(t *testing.T) {
	uses := &memUses{}
	c := issuer(uses).Bind(Binding{Endpoint: "e"}, nil)
	ctx := context.Background()
	out, err := c.CallTool(ctx, CapabilityMint, map[string]any{"ttl": 60, "secret": "open-key", "single_use": true})
	require.NoError(t, err)
	verify := map[string]any{"token": out["token"], "secret": "open-key"}

	first, err := c.CallTool(ctx, CapabilityVerify, verify)
	require.NoError(t, err)
	assert.Equal(t, true, first["ok"])
	assert.Equal(t, true, first["single_use"])
	second, err := c.CallTool(ctx, CapabilityVerify, verify)
	require.NoError(t, err)
	assert.Equal(t, false, second["ok"])
	assert.Equal(t, ReasonUsed, second["reason"])

	uses.err = errors.New("down")
	_, err = issuer(uses).Bind(Binding{Endpoint: "e"}, nil).CallTool(ctx, CapabilityVerify, verify)
	require.ErrorContains(t, err, "down")
	_, err = issuer(nil).Bind(Binding{Endpoint: "e"}, nil).CallTool(ctx, CapabilityVerify, verify)
	require.ErrorContains(t, err, "cannot record")
}

func TestIssuer_Edges(t *testing.T) {
	var none *Issuer
	assert.Nil(t, none.Bind(Binding{}, nil))
	c := issuer(nil).Bind(Binding{ScriptID: "s"}, nil)
	ctx := context.Background()
	_, err := c.CallTool(ctx, "platform.other", map[string]any{"secret": "open-key"})
	require.ErrorContains(t, err, "not a token member")
	_, err = c.CallTool(ctx, CapabilityMint, map[string]any{"secret": "nope", "ttl": 60})
	require.ErrorContains(t, err, "no such secret")
	_, err = c.CallTool(ctx, CapabilityMint, map[string]any{"secret": "open-key", "ttl": "soon"})
	require.ErrorContains(t, err, "reading the arguments")
	_, err = c.CallTool(ctx, CapabilityMint, map[string]any{"secret": "open-key", "ttl": 0})
	require.ErrorContains(t, err, "ttl")
	_, err = c.CallTool(ctx, CapabilityMint, map[string]any{"secret": "open-key", "ttl": 60, "claims": map[string]any{"f": func() {}}})
	require.ErrorContains(t, err, "encoding the arguments")
	clocked, ok := (&Issuer{Keys: keys}).Bind(Binding{}, nil).(*bound)
	require.True(t, ok)
	assert.WithinDuration(t, time.Now(), clocked.now(), time.Minute)
}

// fakeCaller records what a member sent it and answers with out.
type fakeCaller struct {
	name string
	args map[string]any
	out  map[string]any
	err  error
}

func (f *fakeCaller) CallTool(_ context.Context, name string, args map[string]any) (map[string]any, error) {
	f.name, f.args = name, args
	return f.out, f.err
}

// exec runs src with platform.mint_token and platform.verify_token bound to h
// and returns the global result.
func exec(t *testing.T, h Host, src string) (starlark.Value, error) {
	t.Helper()
	env := starlark.StringDict{"platform": &starlarkstruct.Module{Name: "platform", Members: starlark.StringDict{
		"mint_token":   starlark.NewBuiltin(CapabilityMint, h.Mint),
		"verify_token": starlark.NewBuiltin(CapabilityVerify, h.Verify),
	}}}
	globals, err := starlark.ExecFileOptions(&syntax.FileOptions{}, &starlark.Thread{}, "t.star", src, env)
	if err != nil {
		return nil, fmt.Errorf("executing: %w", err)
	}
	return globals["result"], nil
}

// A run sends each call to Live and records it with the secret's name only;
// the token comes back as a string and a verify as a dict.
func TestHost_LiveRecordsTheCall(t *testing.T) {
	live := &fakeCaller{out: map[string]any{"token": "abc.def"}}
	var recorded []string
	h := Host{Ctx: context.Background(), Live: live, Record: func(tool string, args, _ map[string]any, err error) {
		recorded = append(recorded, tool)
		assert.Equal(t, "credit-key", args["secret"])
		assert.NoError(t, err)
	}}
	v, err := exec(t, h, `result = platform.mint_token({"a": 1}, 600, secret = "credit-key", endpoints = ["apply"], single_use = True)`)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("abc.def"), v)
	assert.Equal(t, CapabilityMint, live.name)
	assert.Equal(t, map[string]any{
		"claims": map[string]any{"a": int64(1)}, "ttl": 600, "secret": "credit-key",
		"endpoints": []any{"apply"}, "single_use": true,
	}, live.args)

	live.out = map[string]any{"ok": false, "reason": "expired"}
	v, err = exec(t, h, `result = platform.verify_token("abc.def", secret = "credit-key")`)
	require.NoError(t, err)
	d, ok := v.(*starlark.Dict)
	require.True(t, ok)
	reason, _, _ := d.Get(starlark.String("reason"))
	assert.Equal(t, starlark.String("expired"), reason)
	assert.Equal(t, []string{CapabilityMint, CapabilityVerify}, recorded)
}

// A test answers from the recording and never reaches Live.
func TestHost_TestingAnswersFromTheReplay(t *testing.T) {
	replay := &fakeCaller{out: map[string]any{"token": "recorded"}}
	live := &fakeCaller{}
	v, err := exec(t, Host{Ctx: context.Background(), Testing: true, Replay: replay, Live: live},
		`result = platform.mint_token({}, 60, secret = "k")`)
	require.NoError(t, err)
	assert.Equal(t, starlark.String("recorded"), v)
	assert.Empty(t, live.name)

	_, err = exec(t, Host{Testing: true}, `result = platform.mint_token({}, 60, secret = "k")`)
	require.ErrorContains(t, err, "no recording")
}

func TestHost_Refusals(t *testing.T) {
	ok := &fakeCaller{out: map[string]any{"token": "t"}}
	for name, tc := range map[string]struct {
		h    Host
		src  string
		want string
	}{
		"no issuer":         {Host{}, `platform.mint_token({}, 60, secret = "k")`, "issues no tokens"},
		"no secret":         {Host{Live: ok}, `platform.mint_token({}, 60, secret = "")`, "secret is empty"},
		"no ttl":            {Host{Live: ok}, `platform.mint_token({}, 0, secret = "k")`, "ttl is the seconds"},
		"long ttl":          {Host{Live: ok}, `platform.mint_token({}, 99999999, secret = "k")`, "ttl is the seconds"},
		"bad endpoints":     {Host{Live: ok}, `platform.mint_token({}, 60, secret = "k", endpoints = [1])`, "endpoints must be a list"},
		"bad claims":        {Host{Live: ok}, `platform.mint_token({1: 2}, 60, secret = "k")`, "claims"},
		"missing arg":       {Host{Live: ok}, `platform.mint_token({})`, "missing argument"},
		"no token in reply": {Host{Live: &fakeCaller{out: map[string]any{}}}, `platform.mint_token({}, 60, secret = "k")`, "carries no token"},
		"issuer fails":      {Host{Live: &fakeCaller{err: errors.New("refused")}}, `platform.mint_token({}, 60, secret = "k")`, "refused"},
		"verify no secret":  {Host{Live: ok}, `platform.verify_token("t", secret = "")`, "secret is empty"},
		"verify missing":    {Host{Live: ok}, `platform.verify_token()`, "missing argument"},
		"verify fails":      {Host{Live: &fakeCaller{err: errors.New("down")}}, `platform.verify_token("t", secret = "k")`, "down"},
		"verify bad answer": {Host{Live: &fakeCaller{out: map[string]any{"f": func() {}}}}, `platform.verify_token("t", secret = "k")`, "verify_token"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := exec(t, tc.h, "result = "+tc.src)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// #2361: a run's recording keeps a mint's answer and a verify's argument with
// RecordedToken in place of the token, and a test replays the verify by its
// other arguments whatever token it is handed.
func TestRecordingHoldsNoToken(t *testing.T) {
	type rec struct {
		tool      string
		args, out map[string]any
	}
	var got []rec
	record := func(tool string, args, out map[string]any, _ error) { got = append(got, rec{tool, args, out}) }

	live := &fakeCaller{out: map[string]any{"token": "eyJ.live.token"}}
	_, err := exec(t, Host{Ctx: context.Background(), Live: live, Record: record},
		`result = platform.mint_token(claims={"sub": "a"}, ttl=60, secret="k")`)
	require.NoError(t, err)
	verify := &fakeCaller{out: map[string]any{"valid": true}}
	_, err = exec(t, Host{Ctx: context.Background(), Live: verify, Record: record},
		`result = platform.verify_token(token="eyJ.live.token", secret="k")`)
	require.NoError(t, err)
	assert.Equal(t, "eyJ.live.token", verify.args["token"], "the issuer is sent the real token")

	require.Len(t, got, 2)
	assert.Equal(t, RecordedToken, got[0].out["token"])
	assert.Equal(t, RecordedToken, got[1].args["token"])
	assert.Equal(t, "k", got[1].args["secret"])

	replay := &fakeCaller{out: map[string]any{"valid": true}}
	_, err = exec(t, Host{Ctx: context.Background(), Testing: true, Replay: replay},
		`result = platform.verify_token(token="anything", secret="k")`)
	require.NoError(t, err)
	assert.Equal(t, RecordedToken, replay.args["token"], "a test looks the verify up as it was recorded")
	assert.Nil(t, recordedAnswer(CapabilityMint, nil))
}
