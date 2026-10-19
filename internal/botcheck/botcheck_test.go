package botcheck

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
)

// memSettings is a SettingsStore holding one value, unwritten until Set.
type memSettings struct {
	v   *Settings
	err error
}

func (m *memSettings) Get(context.Context) (settingsjson.Stored[Settings], error) {
	if m.err != nil {
		return settingsjson.Stored[Settings]{}, m.err
	}
	if m.v == nil {
		return settingsjson.Stored[Settings]{}, settingsjson.ErrNotFound
	}
	return settingsjson.Stored[Settings]{Value: *m.v, UpdatedBy: "admin@example.com", UpdatedAt: time.Unix(1700000000, 0)}, nil
}

func (m *memSettings) Set(_ context.Context, v Settings, _ string) error {
	m.v = &v
	return nil
}

// memAnswers models the table's primary key: an id inserted once.
type memAnswers struct {
	mu   sync.Mutex
	used map[string]time.Time
	at   map[string]string
	err  error
}

func newAnswers() *memAnswers {
	return &memAnswers{used: map[string]time.Time{}, at: map[string]string{}}
}

func (m *memAnswers) Use(_ context.Context, id, endpoint string, expires time.Time) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return false, m.err
	}
	if _, ok := m.used[id]; ok {
		return false, nil
	}
	m.used[id], m.at[id] = expires, endpoint
	return true, nil
}

func (m *memAnswers) Recent(_ context.Context, endpoint string, _ time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for _, e := range m.at {
		if e == endpoint {
			n++
		}
	}
	return n, m.err
}

func (*memAnswers) Sweep(context.Context, time.Time) (int64, error) { return 0, nil }

var key = []byte("0123456789abcdef0123456789abcdef")

func lowDifficulty() *Settings {
	s := Defaults()
	s.Difficulty, s.MaxDifficulty, s.RateThreshold = 8, 12, 2
	return &s
}

func powChecker(t *testing.T, s *Settings) (*Checker, *memAnswers) {
	t.Helper()
	answers := newAnswers()
	return &Checker{Settings: &memSettings{v: s}, Answers: answers, Key: key}, answers
}

func TestDefaultsAreProofOfWorkAndValid(t *testing.T) {
	s := Defaults()
	require.NoError(t, s.Validate())
	assert.Equal(t, ProviderPoW, s.Provider)
	assert.True(t, s.PublicAllowed())
	none := Settings{Provider: ProviderNone}
	assert.False(t, none.PublicAllowed())
	none.AllowPublicWithoutCheck = true
	assert.True(t, none.PublicAllowed())
}

func TestInputApply(t *testing.T) {
	str := func(s string) *string { return &s }
	n := func(i int) *int { return &i }
	yes := true
	out, err := Input{
		Provider: str(" turnstile "), SiteKey: str("1x00000000000000000000AA"), SecretName: str("turnstile-key"),
		AllowPublicWithoutCheck: &yes, Difficulty: n(10), MaxDifficulty: n(20), RateThreshold: n(5), ChallengeTTLSeconds: n(60),
	}.Apply(Defaults())
	require.NoError(t, err)
	assert.Equal(t, ProviderTurnstile, out.Provider)
	assert.Equal(t, 10, out.Difficulty)
	assert.True(t, out.AllowPublicWithoutCheck)

	for name, in := range map[string]Input{
		"unknown provider": {Provider: str("recaptcha")},
		"no site key":      {Provider: str(ProviderHCaptcha), SecretName: str("k")},
		"no secret":        {Provider: str(ProviderHCaptcha), SiteKey: str("k")},
		"long site key":    {SiteKey: str(strings.Repeat("k", 201))},
		"long secret name": {SecretName: str(strings.Repeat("k", 129))},
		"easy":             {Difficulty: n(2)},
		"max under base":   {MaxDifficulty: n(10)},
		"rate":             {RateThreshold: n(0)},
		"ttl":              {ChallengeTTLSeconds: n(2)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := in.Apply(Defaults())
			require.Error(t, err)
		})
	}
}

func TestReadDefaultsWhenUnwrittenAndReportsTheWriter(t *testing.T) {
	v, err := Read(context.Background(), &memSettings{})
	require.NoError(t, err)
	assert.Equal(t, Defaults(), v.Settings)
	assert.Empty(t, v.UpdatedBy)

	s := Defaults()
	v, err = Read(context.Background(), &memSettings{v: &s})
	require.NoError(t, err)
	assert.Equal(t, "admin@example.com", v.UpdatedBy)
	assert.NotEmpty(t, v.UpdatedAt)

	_, err = Read(context.Background(), &memSettings{err: errors.New("boom")})
	require.ErrorContains(t, err, "reading the bot check settings")
}

// The acceptance shape of #2173 for the default provider: a solved
// challenge is accepted once, and refused again, expired, altered or on
// another endpoint.
func TestProofOfWork_AcceptsOnceAndRefusesTheRest(t *testing.T) {
	c, _ := powChecker(t, lowDifficulty())
	ctx := context.Background()
	ch, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, ProviderPoW, ch.Provider)
	assert.Equal(t, 8, ch.Difficulty)
	answer := solve(ch.Token, ch.Difficulty)

	require.NoError(t, c.Verify(ctx, "orders", answer))
	require.ErrorIs(t, c.Verify(ctx, "orders", answer), ErrReplayed, "the same answer sent again")

	other, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	require.ErrorIs(t, c.Verify(ctx, "contact", solve(other.Token, other.Difficulty)), ErrOtherEndpoint)

	late, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	c.Now = func() time.Time { return time.Now().Add(time.Hour) }
	require.ErrorIs(t, c.Verify(ctx, "orders", solve(late.Token, late.Difficulty)), ErrExpired)
	c.Now = nil

	fresh, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	solved := solve(fresh.Token, fresh.Difficulty)
	tok, counter, _ := strings.Cut(solved, "~")
	for name, bad := range map[string]string{
		"no counter":     tok,
		"no signature":   strings.Split(tok, ".")[0] + "~" + counter,
		"bad signature":  strings.Split(tok, ".")[0] + ".AAAA~" + counter,
		"not a number":   tok + "~x",
		"altered claims": "e30." + strings.Split(tok, ".")[1] + "~" + counter,
		"too long":       strings.Repeat("a", maxAnswerLen+1),
	} {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, c.Verify(ctx, "orders", bad), ErrInvalid)
		})
	}
	require.ErrorIs(t, c.Verify(ctx, "orders", ""), ErrMissing)
}

// A wrong counter is refused: the answer has to carry the work.
func TestProofOfWork_RefusesAnAnswerWithoutTheWork(t *testing.T) {
	c, _ := powChecker(t, lowDifficulty())
	ch, err := c.Issue(context.Background(), "orders")
	require.NoError(t, err)
	for i := range 1000 {
		counter := strings.Repeat("9", 1) + string(rune('0'+i%10))
		if !worked(ch.Token, counter, ch.Difficulty) {
			require.ErrorIs(t, c.Verify(context.Background(), "orders", ch.Token+"~"+counter), ErrInvalid)
			return
		}
	}
	t.Fatal("found no counter without the work")
}

// A burst past the threshold raises the difficulty, and the raised challenge
// solved is still accepted.
func TestProofOfWork_DifficultyRisesWithTheRecentRate(t *testing.T) {
	c, _ := powChecker(t, lowDifficulty())
	ctx := context.Background()
	quiet, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	for range 8 {
		ch, err := c.Issue(ctx, "orders")
		require.NoError(t, err)
		require.NoError(t, c.Verify(ctx, "orders", solve(ch.Token, ch.Difficulty)))
	}
	raised, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	assert.Greater(t, raised.Difficulty, quiet.Difficulty)
	assert.Equal(t, 10, raised.Difficulty, "8 answers against a threshold of 2 is two doublings: two bits")
	require.NoError(t, c.Verify(ctx, "orders", solve(raised.Token, raised.Difficulty)))

	other, err := c.Issue(ctx, "contact")
	require.NoError(t, err)
	assert.Equal(t, quiet.Difficulty, other.Difficulty, "one endpoint's flood does not raise another's")
}

func TestDifficultyFor(t *testing.T) {
	s := Settings{Difficulty: 16, MaxDifficulty: 20, RateThreshold: 30}
	assert.Equal(t, 16, difficultyFor(s, 0))
	assert.Equal(t, 16, difficultyFor(s, 30))
	assert.Equal(t, 17, difficultyFor(s, 31))
	assert.Equal(t, 17, difficultyFor(s, 60))
	assert.Equal(t, 18, difficultyFor(s, 61))
	assert.Equal(t, 20, difficultyFor(s, 1_000_000), "capped at the most")
}

func TestProofOfWork_WithoutAKeyIsUnavailable(t *testing.T) {
	c, _ := powChecker(t, lowDifficulty())
	c.Key = nil
	_, err := c.Issue(context.Background(), "orders")
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, c.Verify(context.Background(), "orders", "x"), ErrUnavailable)
	assert.False(t, Refusal(ErrUnavailable), "the check could not run; the caller is not refused for it")
	ok, err := c.PublicAllowed(context.Background())
	require.NoError(t, err)
	assert.False(t, ok, "proof of work with no key to sign is no bot check")
}

func TestNoneChecksNothingAndIsPublicOnlyWithTheOverride(t *testing.T) {
	ctx := context.Background()
	s := Settings{Provider: ProviderNone}
	c := &Checker{Settings: &memSettings{v: &s}, Answers: newAnswers()}
	ch, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, ProviderNone, ch.Provider)
	require.NoError(t, c.Verify(ctx, "orders", ""))
	ok, err := c.PublicAllowed(ctx)
	require.NoError(t, err)
	assert.False(t, ok)
	s.AllowPublicWithoutCheck = true
	ok, err = c.PublicAllowed(ctx)
	require.NoError(t, err)
	assert.True(t, ok)
}

func TestSettingsReadFailureIsPassedOn(t *testing.T) {
	c := &Checker{Settings: &memSettings{err: errors.New("boom")}, Answers: newAnswers(), Key: key}
	_, err := c.Issue(context.Background(), "x")
	require.Error(t, err)
	require.Error(t, c.Verify(context.Background(), "x", "y"))
	_, err = c.PublicAllowed(context.Background())
	require.Error(t, err)
}

func TestAnswerStoreFailureIsPassedOn(t *testing.T) {
	c, answers := powChecker(t, lowDifficulty())
	ch, err := c.Issue(context.Background(), "orders")
	require.NoError(t, err)
	answers.err = errors.New("boom")
	require.Error(t, c.Verify(context.Background(), "orders", solve(ch.Token, ch.Difficulty)))
	_, err = c.Issue(context.Background(), "orders")
	require.ErrorContains(t, err, "recent rate")
}

// siteverify is a provider's verification endpoint: it accepts goodToken
// once, answering timeout-or-duplicate after, as Turnstile does.
func siteverify(t *testing.T, secret string) *httptest.Server {
	t.Helper()
	var mu sync.Mutex
	seen := map[string]bool{}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, r.ParseForm())
		mu.Lock()
		defer mu.Unlock()
		token := r.PostForm.Get("response")
		switch {
		case r.PostForm.Get("secret") != secret || token != "goodToken":
			_, _ = w.Write([]byte(`{"success":false,"error-codes":["invalid-input-response"]}`))
		case seen[token]:
			_, _ = w.Write([]byte(`{"success":false,"error-codes":["timeout-or-duplicate"]}`))
		default:
			seen[token] = true
			_, _ = w.Write([]byte(`{"success":true}`))
		}
	}))
}

func TestProvider_AcceptsOnlyWhatTheProviderVerifiesAndOnlyOnce(t *testing.T) {
	srv := siteverify(t, "the-secret")
	defer srv.Close()
	v := NewVerifier()
	v.urls[ProviderTurnstile] = srv.URL
	s := Settings{Provider: ProviderTurnstile, SiteKey: "site", SecretName: "turnstile-key"}
	answers := newAnswers()
	c := &Checker{
		Settings: &memSettings{v: &s}, Answers: answers, Verifier: v,
		Secret: func(_ context.Context, name string) (string, error) {
			if name != "turnstile-key" {
				return "", errors.New("no such secret")
			}
			return "the-secret", nil
		},
	}
	ctx := context.Background()
	ch, err := c.Issue(ctx, "orders")
	require.NoError(t, err)
	assert.Equal(t, Challenge{Provider: ProviderTurnstile, SiteKey: "site"}, ch)

	require.ErrorIs(t, c.Verify(ctx, "orders", "badToken"), ErrInvalid)
	require.NoError(t, c.Verify(ctx, "orders", "goodToken"))
	require.ErrorIs(t, c.Verify(ctx, "orders", "goodToken"), ErrReplayed)
	require.ErrorIs(t, c.Verify(ctx, "orders", strings.Repeat("t", maxAnswerLen+1)), ErrInvalid)

	s.SecretName = "missing"
	require.ErrorContains(t, c.Verify(ctx, "orders", "goodToken"), "reading the turnstile secret key")
}

// A token the provider verified but this platform recorded already is
// refused, which holds across replicas even where a provider would accept a
// token twice.
func TestProvider_ARecordedTokenIsReplayed(t *testing.T) {
	v := &stubVerifier{}
	s := Settings{Provider: ProviderHCaptcha, SiteKey: "site", SecretName: "k"}
	c := &Checker{
		Settings: &memSettings{v: &s}, Answers: newAnswers(), Verifier: v,
		Secret: func(context.Context, string) (string, error) { return "x", nil },
	}
	require.NoError(t, c.Verify(context.Background(), "orders", "tok"))
	require.ErrorIs(t, c.Verify(context.Background(), "orders", "tok"), ErrReplayed)
}

type stubVerifier struct{}

func (stubVerifier) Verify(context.Context, string, string, string) error { return nil }

func TestVerifier_ReportsAProviderThatDidNotAnswerProperly(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"500":      func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
		"not json": func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("<html>")) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(handler)
			defer srv.Close()
			v := NewVerifier()
			v.urls[ProviderHCaptcha] = srv.URL
			err := v.Verify(context.Background(), ProviderHCaptcha, "s", "t")
			require.Error(t, err)
			assert.False(t, Refusal(err), "a provider that did not answer is not the caller's refusal")
		})
	}
	v := NewVerifier()
	require.Error(t, v.Verify(context.Background(), ProviderPoW, "s", "t"))
	v.urls[ProviderHCaptcha] = "http://127.0.0.1:1"
	require.ErrorContains(t, v.Verify(context.Background(), ProviderHCaptcha, "s", "t"), "did not answer")
}

func TestRefusal(t *testing.T) {
	for _, err := range []error{ErrMissing, ErrInvalid, ErrExpired, ErrReplayed, ErrOtherEndpoint} {
		assert.True(t, Refusal(err), err.Error())
	}
	assert.False(t, Refusal(errors.New("boom")))
}

func TestSolverScriptIsEmbedded(t *testing.T) {
	assert.Contains(t, string(SolverJS), "window.PlatformBotCheck")
	assert.Contains(t, string(SolverJS), `"X-Bot-Check"`)
}

func TestKey(t *testing.T) {
	assert.Nil(t, Key(false, "c2VjcmV0"))
	assert.Nil(t, Key(true, ""))
	assert.Nil(t, Key(true, "not base64!"))
	k := Key(true, "c2VjcmV0")
	assert.Len(t, k, 32)
	assert.Equal(t, k, Key(true, "c2VjcmV0"), "every replica derives the same key")
}

// solve finds the counter that answers token at difficulty and returns the
// answer a request carries, what the platform's browser script computes.
func solve(token string, difficulty int) string {
	for i := uint64(0); ; i++ {
		c := strconv.FormatUint(i, counterBase)
		if worked(token, c, difficulty) {
			return token + answerSeparator + c
		}
	}
}
