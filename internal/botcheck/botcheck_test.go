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
)

// memChecks is a Store over a map, modeling the table's primary key and the
// endpoints' foreign key through used.
type memChecks struct {
	mu   sync.Mutex
	m    map[string]Check
	used map[string][]string
	err  error
}

func checks(cs ...Check) *memChecks {
	m := &memChecks{m: map[string]Check{}, used: map[string][]string{}}
	for _, c := range cs {
		m.m[c.Name] = c
	}
	return m
}

func (m *memChecks) List(context.Context) ([]Check, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Check, 0, len(m.m))
	for _, c := range m.m {
		out = append(out, c)
	}
	return out, m.err
}

func (m *memChecks) Get(_ context.Context, name string) (Check, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return Check{}, m.err
	}
	c, ok := m.m[name]
	if !ok {
		return Check{}, ErrNotFound
	}
	return c, nil
}

func (m *memChecks) Put(_ context.Context, c Check, author string) (Check, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c.UpdatedBy = author
	m.m[c.Name] = c
	return c, m.err
}

func (m *memChecks) Delete(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.m[name]; !ok {
		return ErrNotFound
	}
	if len(m.used[name]) > 0 {
		return &InUseError{Name: name, Endpoints: m.used[name]}
	}
	delete(m.m, name)
	return nil
}

func (m *memChecks) UsedBy(context.Context) (map[string][]string, error) { return m.used, m.err }

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

func lowDifficulty() Check {
	c := New(DefaultName)
	c.Difficulty, c.MaxDifficulty, c.RateThreshold = 8, 12, 2
	return c
}

func powChecker(t *testing.T, c Check) (*Checker, *memAnswers) {
	t.Helper()
	answers := newAnswers()
	return &Checker{Checks: checks(c), Answers: answers, Key: key}, answers
}

func TestNewIsProofOfWorkAndValid(t *testing.T) {
	c := New("forms")
	require.NoError(t, c.Validate())
	assert.Equal(t, ProviderPoW, c.Provider)
	assert.False(t, c.NeedsBrowser())
	c.Provider = ProviderTurnstile
	assert.True(t, c.NeedsBrowser())
}

func TestInputApply(t *testing.T) {
	str := func(s string) *string { return &s }
	n := func(i int) *int { return &i }
	out, err := Input{
		Provider: str(" turnstile "), SiteKey: str("1x00000000000000000000AA"), SecretName: str("turnstile-key"),
		Difficulty: n(10), MaxDifficulty: n(20), RateThreshold: n(5), ChallengeTTLSeconds: n(60),
	}.Apply(New("forms"))
	require.NoError(t, err)
	assert.Equal(t, ProviderTurnstile, out.Provider)
	assert.Equal(t, 10, out.Difficulty)
	assert.Equal(t, "forms", out.Name)

	for name, in := range map[string]Input{
		"none is no provider": {Provider: str("none")},
		"unknown provider":    {Provider: str("recaptcha")},
		"no site key":         {Provider: str(ProviderHCaptcha), SecretName: str("k")},
		"no secret":           {Provider: str(ProviderHCaptcha), SiteKey: str("k")},
		"long site key":       {SiteKey: str(strings.Repeat("k", 201))},
		"long secret name":    {SecretName: str(strings.Repeat("k", 129))},
		"easy":                {Difficulty: n(2)},
		"max under base":      {MaxDifficulty: n(10)},
		"rate":                {RateThreshold: n(0)},
		"ttl":                 {ChallengeTTLSeconds: n(2)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := in.Apply(New("forms"))
			require.Error(t, err)
		})
	}
}

func TestValidateName(t *testing.T) {
	for _, ok := range []string{"default", "forms-2", "a_b"} {
		require.NoError(t, ValidateName(ok), ok)
	}
	for _, bad := range []string{"", "Forms", "-x", "a b", strings.Repeat("a", 64)} {
		require.Error(t, ValidateName(bad), bad)
	}
}

func TestInUseErrorNamesTheEndpoints(t *testing.T) {
	err := &InUseError{Name: "default", Endpoints: []string{"contact", "orders"}}
	assert.Equal(t, `bot check "default" is used by the endpoints contact, orders: bind them to another check first`, err.Error())
}

func TestUsable(t *testing.T) {
	ctx := context.Background()
	c, _ := powChecker(t, lowDifficulty())
	require.NoError(t, c.Usable(ctx, DefaultName))
	require.ErrorContains(t, c.Usable(ctx, "missing"), `no bot check is named "missing"`)
	c.Key = nil
	require.ErrorContains(t, c.Usable(ctx, DefaultName), "cannot sign one")
	c.Checks = &memChecks{err: errors.New("boom")}
	require.ErrorContains(t, c.Usable(ctx, DefaultName), "boom")
}

// The acceptance shape of #2173 for the default provider: a solved
// challenge is accepted once, and refused again, expired, altered or on
// another endpoint.
func TestProofOfWork_AcceptsOnceAndRefusesTheRest(t *testing.T) {
	c, _ := powChecker(t, lowDifficulty())
	ctx := context.Background()
	ch, err := c.Issue(ctx, DefaultName, "orders")
	require.NoError(t, err)
	assert.Equal(t, ProviderPoW, ch.Provider)
	assert.Equal(t, 8, ch.Difficulty)
	answer := solve(ch.Token, ch.Difficulty)

	require.NoError(t, c.Verify(ctx, DefaultName, "orders", answer))
	require.ErrorIs(t, c.Verify(ctx, DefaultName, "orders", answer), ErrReplayed, "the same answer sent again")

	other, err := c.Issue(ctx, DefaultName, "orders")
	require.NoError(t, err)
	require.ErrorIs(t, c.Verify(ctx, DefaultName, "contact", solve(other.Token, other.Difficulty)), ErrOtherEndpoint)

	late, err := c.Issue(ctx, DefaultName, "orders")
	require.NoError(t, err)
	c.Now = func() time.Time { return time.Now().Add(time.Hour) }
	require.ErrorIs(t, c.Verify(ctx, DefaultName, "orders", solve(late.Token, late.Difficulty)), ErrExpired)
	c.Now = nil

	fresh, err := c.Issue(ctx, DefaultName, "orders")
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
			require.ErrorIs(t, c.Verify(ctx, DefaultName, "orders", bad), ErrInvalid)
		})
	}
	require.ErrorIs(t, c.Verify(ctx, DefaultName, "orders", ""), ErrMissing)
}

// A wrong counter is refused: the answer has to carry the work.
func TestProofOfWork_RefusesAnAnswerWithoutTheWork(t *testing.T) {
	c, _ := powChecker(t, lowDifficulty())
	ch, err := c.Issue(context.Background(), DefaultName, "orders")
	require.NoError(t, err)
	for i := range 1000 {
		counter := strings.Repeat("9", 1) + string(rune('0'+i%10))
		if !worked(ch.Token, counter, ch.Difficulty) {
			require.ErrorIs(t, c.Verify(context.Background(), DefaultName, "orders", ch.Token+"~"+counter), ErrInvalid)
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
	quiet, err := c.Issue(ctx, DefaultName, "orders")
	require.NoError(t, err)
	for range 8 {
		ch, err := c.Issue(ctx, DefaultName, "orders")
		require.NoError(t, err)
		require.NoError(t, c.Verify(ctx, DefaultName, "orders", solve(ch.Token, ch.Difficulty)))
	}
	raised, err := c.Issue(ctx, DefaultName, "orders")
	require.NoError(t, err)
	assert.Greater(t, raised.Difficulty, quiet.Difficulty)
	assert.Equal(t, 10, raised.Difficulty, "8 answers against a threshold of 2 is two doublings: two bits")
	require.NoError(t, c.Verify(ctx, DefaultName, "orders", solve(raised.Token, raised.Difficulty)))

	other, err := c.Issue(ctx, DefaultName, "contact")
	require.NoError(t, err)
	assert.Equal(t, quiet.Difficulty, other.Difficulty, "one endpoint's flood does not raise another's")
}

func TestDifficultyFor(t *testing.T) {
	s := Check{Difficulty: 16, MaxDifficulty: 20, RateThreshold: 30}
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
	_, err := c.Issue(context.Background(), DefaultName, "orders")
	require.ErrorIs(t, err, ErrUnavailable)
	require.ErrorIs(t, c.Verify(context.Background(), DefaultName, "orders", "x"), ErrUnavailable)
	assert.False(t, Refusal(ErrUnavailable), "the check could not run; the caller is not refused for it")
}

func TestCheckReadFailureIsPassedOn(t *testing.T) {
	c := &Checker{Checks: &memChecks{err: errors.New("boom")}, Answers: newAnswers(), Key: key}
	_, err := c.Issue(context.Background(), DefaultName, "x")
	require.Error(t, err)
	require.Error(t, c.Verify(context.Background(), DefaultName, "x", "y"))
}

// Two proof-of-work checks issue at their own difficulty, and each refuses
// the other's answer on the same endpoint (#2259).
func TestProofOfWork_EachCheckIssuesItsOwnAndRefusesAnothers(t *testing.T) {
	easy, hard := lowDifficulty(), lowDifficulty()
	easy.Name, hard.Name = "easy", "hard"
	hard.Difficulty, hard.MaxDifficulty = 10, 12
	c := &Checker{Checks: checks(easy, hard), Answers: newAnswers(), Key: key}
	ctx := context.Background()
	e, err := c.Issue(ctx, "easy", "orders")
	require.NoError(t, err)
	h, err := c.Issue(ctx, "hard", "orders")
	require.NoError(t, err)
	assert.Equal(t, 8, e.Difficulty)
	assert.Equal(t, 10, h.Difficulty)
	assert.Equal(t, "hard", h.Check)

	require.ErrorIs(t, c.Verify(ctx, "hard", "orders", solve(e.Token, e.Difficulty)), ErrOtherCheck)
	require.ErrorIs(t, c.Verify(ctx, "easy", "orders", solve(h.Token, h.Difficulty)), ErrOtherCheck)
	require.NoError(t, c.Verify(ctx, "easy", "orders", solve(e.Token, e.Difficulty)))
	require.NoError(t, c.Verify(ctx, "hard", "orders", solve(h.Token, h.Difficulty)))
	require.ErrorIs(t, c.Verify(ctx, "gone", "orders", "x"), ErrNotFound)
}

func TestAnswerStoreFailureIsPassedOn(t *testing.T) {
	c, answers := powChecker(t, lowDifficulty())
	ch, err := c.Issue(context.Background(), DefaultName, "orders")
	require.NoError(t, err)
	answers.err = errors.New("boom")
	require.Error(t, c.Verify(context.Background(), DefaultName, "orders", solve(ch.Token, ch.Difficulty)))
	_, err = c.Issue(context.Background(), DefaultName, "orders")
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
	s := Check{Name: "forms", Provider: ProviderTurnstile, SiteKey: "site", SecretName: "turnstile-key"}
	answers := newAnswers()
	store := checks(s)
	c := &Checker{
		Checks: store, Answers: answers, Verifier: v,
		Secret: func(_ context.Context, name string) (string, error) {
			if name != "turnstile-key" {
				return "", errors.New("no such secret")
			}
			return "the-secret", nil
		},
	}
	ctx := context.Background()
	ch, err := c.Issue(ctx, "forms", "orders")
	require.NoError(t, err)
	assert.Equal(t, Challenge{Check: "forms", Provider: ProviderTurnstile, SiteKey: "site"}, ch)

	require.ErrorIs(t, c.Verify(ctx, "forms", "orders", "badToken"), ErrInvalid)
	require.ErrorIs(t, c.Verify(ctx, "forms", "orders", ""), ErrMissing)
	require.NoError(t, c.Verify(ctx, "forms", "orders", "goodToken"))
	require.ErrorIs(t, c.Verify(ctx, "forms", "orders", "goodToken"), ErrReplayed)
	require.ErrorIs(t, c.Verify(ctx, "forms", "orders", strings.Repeat("t", maxAnswerLen+1)), ErrInvalid)

	s.SecretName = "missing"
	store.m["forms"] = s
	require.ErrorContains(t, c.Verify(ctx, "forms", "orders", "goodToken"), "reading the turnstile secret key")
}

// A token the provider verified but this platform recorded already is
// refused, which holds across replicas even where a provider would accept a
// token twice.
func TestProvider_ARecordedTokenIsReplayed(t *testing.T) {
	v := &stubVerifier{}
	s := Check{Name: "h", Provider: ProviderHCaptcha, SiteKey: "site", SecretName: "k"}
	c := &Checker{
		Checks: checks(s), Answers: newAnswers(), Verifier: v,
		Secret: func(context.Context, string) (string, error) { return "x", nil },
	}
	require.NoError(t, c.Verify(context.Background(), "h", "orders", "tok"))
	require.ErrorIs(t, c.Verify(context.Background(), "h", "orders", "tok"), ErrReplayed)
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
	for _, err := range []error{ErrMissing, ErrInvalid, ErrExpired, ErrReplayed, ErrOtherEndpoint, ErrOtherCheck} {
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
