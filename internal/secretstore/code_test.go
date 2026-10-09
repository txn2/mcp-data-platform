package secretstore

import (
	"context"
	"database/sql"
	"encoding/base32"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/internal/totp"
)

// rfcSeed is RFC 6238's SHA1 test seed, "12345678901234567890", in base32.
var rfcSeed = base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString([]byte("12345678901234567890"))

// fakeClock is a clock a test moves: sleeping advances it by the wait, so a
// fill that waits for the next period returns at once with the clock there.
type fakeClock struct {
	t     time.Time
	slept []time.Duration
}

func (c *fakeClock) now() time.Time { return c.t }

func (c *fakeClock) sleep(_ context.Context, d time.Duration) error {
	c.slept = append(c.slept, d)
	c.t = c.t.Add(d)
	return nil
}

// totpRow is a totp secret's row as the lookup reads it.
func totpRow(now time.Time, algorithm string, digits, period int) *sqlmock.Rows {
	return sqlmock.NewRows(append(append([]string(nil), metaCols...), "value")).
		AddRow("mfa", "", "{grid}", "{}", "", "", now, now, "totp", algorithm, digits, period, "enc:"+rfcSeed)
}

const claimSQL = `UPDATE gateway_secrets SET totp_last_period`

func rfcCode(t *testing.T, p totp.Params, counter int64) string {
	t.Helper()
	code, err := totp.Code(rfcSeed, p, counter)
	require.NoError(t, err)
	return code
}

func TestPutStoresAnAuthenticatorSeed(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	s, mock := newMock(t, prefixEncryptor{})
	uri := "otpauth://totp/Vendor:ops?secret=" + rfcSeed + "&algorithm=SHA256&digits=8&period=60"

	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("mfa").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(`INSERT INTO gateway_secrets`).
		WithArgs("mfa", "", "enc:"+rfcSeed, `{"grid"}`, "{}", "", "totp", "SHA256", 8, 60).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("mfa").
		WillReturnRows(sqlmock.NewRows(metaCols).AddRow("mfa", "", "{grid}", "{}", "", "", now, now, "totp", "SHA256", 8, 60))
	sec, created, err := s.Put(ctx, Write{Name: "mfa", Kind: KindTOTP, Value: &uri, AllowConnections: []string{"grid"}})
	require.NoError(t, err)
	assert.True(t, created)
	require.NotNil(t, sec.TOTP)
	assert.Equal(t, totp.Params{Algorithm: "SHA256", Digits: 8, Period: 60}, *sec.TOTP)

	// A rescope keeps the stored kind, which a write that names none means.
	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("mfa").
		WillReturnRows(sqlmock.NewRows(metaCols).AddRow("mfa", "", "{grid}", "{}", "", "", now, now, "totp", "SHA256", 8, 60))
	mock.ExpectExec(`UPDATE gateway_secrets SET`).
		WithArgs("mfa", "", sql.NullString{}, `{"grid"}`, "{}", "", "totp", "", 0, 0).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("mfa").
		WillReturnRows(sqlmock.NewRows(metaCols).AddRow("mfa", "", "{grid}", "{}", "", "", now, now, "totp", "SHA256", 8, 60))
	_, _, err = s.Put(ctx, Write{Name: "mfa", AllowConnections: []string{"grid"}})
	require.NoError(t, err)

	// Changing the kind needs a value of the new kind.
	mock.ExpectQuery(`FROM gateway_secrets WHERE name`).WithArgs("mfa").
		WillReturnRows(sqlmock.NewRows(metaCols).AddRow("mfa", "", "{grid}", "{}", "", "", now, now, "totp", "SHA256", 8, 60))
	_, _, err = s.Put(ctx, Write{Name: "mfa", Kind: KindValue, AllowConnections: []string{"grid"}})
	require.ErrorIs(t, err, ErrInvalid)
	require.ErrorContains(t, err, "needs a new value")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestValidateAuthenticatorSeeds(t *testing.T) {
	hotp := "otpauth://hotp/V:x?secret=" + rfcSeed + "&counter=1"
	notBase32 := "not!base32"
	short := "abc"
	cases := map[string]struct {
		w    Write
		want string
	}{
		"hotp":         {Write{Name: "a", Kind: KindTOTP, Value: &hotp, AllowConnections: []string{"g"}}, "hotp"},
		"not base32":   {Write{Name: "a", Kind: KindTOTP, Value: &notBase32, AllowConnections: []string{"g"}}, "not base32"},
		"no seed":      {Write{Name: "a", Kind: KindTOTP, AllowConnections: []string{"g"}}, "seed is required"},
		"unknown kind": {Write{Name: "a", Kind: "hotp", Value: &short, AllowConnections: []string{"g"}}, `kind "hotp"`},
		"short seed":   {Write{Name: "a", Kind: KindTOTP, Value: &short, AllowConnections: []string{"g"}}, "shorter than"},
	}
	for name, c := range cases {
		err := Validate(c.w, true)
		require.ErrorIs(t, err, ErrInvalid, name)
		assert.Contains(t, err.Error(), c.want, name)
	}
}

// TestCodeIsIssuedOncePerPeriod is #2065's reuse rule against a shared row:
// the first fill in a period sends its code, the second claims and waits for
// the next period, and a third in the same period is refused.
func TestCodeIsIssuedOncePerPeriod(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_111_111_111, 0)} // 1s into a 30s period's 21st second
	s, mock := newMock(t, prefixEncryptor{})
	s.WithClock(clock.now, clock.sleep)
	p := totp.Defaults()
	first := p.Counter(clock.t)

	// First call: claims this period. Two placeholders in it share one code.
	ctx, redactor := secretref.WithRedactor(context.Background())
	lookup := s.Lookup(ctx, "grid", "analyst")
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WithArgs("mfa", first).WillReturnResult(sqlmock.NewResult(0, 1))
	code, err := lookup(secretref.TOTPName("mfa"))
	require.NoError(t, err)
	assert.Equal(t, rfcCode(t, p, first), code)
	again, err := lookup(secretref.TOTPName("mfa"))
	require.NoError(t, err)
	assert.Equal(t, code, again)
	assert.Empty(t, clock.slept)
	// The seed is redacted from the response; the code is not.
	assert.Equal(t, secretref.Redaction("mfa"), redactor.String(rfcSeed))
	assert.Equal(t, code, redactor.String(code))

	// Second call in the same period: waits for the next and sends its code.
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WithArgs("mfa", first).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WithArgs("mfa", first+1).WillReturnResult(sqlmock.NewResult(0, 1))
	start := clock.t
	second, err := s.Lookup(context.Background(), "grid", "analyst")(secretref.TOTPName("mfa"))
	require.NoError(t, err)
	assert.Equal(t, rfcCode(t, p, first+1), second)
	assert.NotEqual(t, code, second)
	assert.Equal(t, []time.Duration{p.Start(first + 1).Sub(start)}, clock.slept, "waited until the next period began")

	// Third call, in that period: this one and the next are both claimed.
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WithArgs("mfa", first+1).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WithArgs("mfa", first+2).WillReturnResult(sqlmock.NewResult(0, 0))
	_, err = s.Lookup(context.Background(), "grid", "analyst")(secretref.TOTPName("mfa"))
	require.ErrorContains(t, err, "try again after")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCodeParametersFromTheURI(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_234_567_890, 0)}
	s, mock := newMock(t, prefixEncryptor{})
	s.WithClock(clock.now, clock.sleep)
	p := totp.Params{Algorithm: "SHA256", Digits: 8, Period: 60}
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA256", 8, 60))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WithArgs("mfa", p.Counter(clock.t)).WillReturnResult(sqlmock.NewResult(0, 1))
	code, err := s.Lookup(context.Background(), "grid", "")(secretref.TOTPName("mfa"))
	require.NoError(t, err)
	assert.Len(t, code, 8)
	assert.Equal(t, rfcCode(t, p, p.Counter(clock.t)), code)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestKindsDoNotCross(t *testing.T) {
	now := time.Now()
	s, mock := newMock(t, prefixEncryptor{})
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(now, "SHA1", 6, 30))
	_, err := s.Lookup(context.Background(), "grid", "")("mfa")
	require.ErrorContains(t, err, "is an authenticator seed, which is never sent; write {{totp:mfa}}")

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(now, "enc:hunter22", "{}"))
	_, err = s.Lookup(context.Background(), "grid", "")(secretref.TOTPName("pw"))
	require.ErrorContains(t, err, "holds a value, not an authenticator seed")

	// The scope holds for a code as for a value, before anything is claimed.
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(now, "SHA1", 6, 30))
	_, err = s.Lookup(context.Background(), "elsewhere", "")(secretref.TOTPName("mfa"))
	require.ErrorContains(t, err, `may not be sent through connection "elsewhere"`)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(now, "SHA1", 6, 30))
	_, err = s.ConnectionValue(context.Background(), "mfa", "grid")
	require.ErrorContains(t, err, "never sent")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestClaimFailures(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_111_111_111, 0)}
	s, mock := newMock(t, prefixEncryptor{})
	s.WithClock(clock.now, clock.sleep)
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WillReturnError(errors.New("down"))
	_, err := s.Lookup(context.Background(), "grid", "")(secretref.TOTPName("mfa"))
	require.ErrorContains(t, err, "recording the one-time code period")

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WillReturnError(errors.New("down"))
	_, err = s.Lookup(context.Background(), "grid", "")(secretref.TOTPName("mfa"))
	require.ErrorContains(t, err, "recording the one-time code period")

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WillReturnResult(sqlmock.NewErrorResult(errors.New("no count")))
	_, err = s.Lookup(context.Background(), "grid", "")(secretref.TOTPName("mfa"))
	require.ErrorContains(t, err, "recording the one-time code period")

	// A wait cut short by the call's own deadline is the call's failure.
	s.WithClock(clock.now, func(context.Context, time.Duration) error { return context.DeadlineExceeded })
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectExec(regexp.QuoteMeta(claimSQL)).WillReturnResult(sqlmock.NewResult(0, 1))
	_, err = s.Lookup(context.Background(), "grid", "")(secretref.TOTPName("mfa"))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCurrentCodeIssuesNothing(t *testing.T) {
	clock := &fakeClock{t: time.Unix(65, 0)}
	s, mock := newMock(t, prefixEncryptor{})
	s.WithClock(clock.now, clock.sleep)
	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(totpRow(clock.t, "SHA1", 6, 30))
	got, err := s.Code(context.Background(), "mfa")
	require.NoError(t, err)
	assert.Equal(t, rfcCode(t, totp.Defaults(), 2), got.Code)
	assert.Equal(t, 25, got.SecondsLeft)
	assert.Equal(t, totp.Defaults(), got.Params)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("pw").WillReturnRows(valueRow(clock.t, "enc:hunter22", "{}"))
	_, err = s.Code(context.Background(), "pw")
	require.ErrorIs(t, err, ErrNotTOTP)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("none").WillReturnError(sql.ErrNoRows)
	_, err = s.Code(context.Background(), "none")
	require.ErrorIs(t, err, ErrNotFound)

	mock.ExpectQuery(`, value FROM gateway_secrets`).WithArgs("mfa").WillReturnRows(
		sqlmock.NewRows(append(append([]string(nil), metaCols...), "value")).
			AddRow("mfa", "", "{grid}", "{}", "", "", clock.t, clock.t, "totp", "SHA1", 6, 30, "enc:!!"))
	_, err = s.Code(context.Background(), "mfa")
	require.ErrorContains(t, err, "computing the code")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSleepCtx(t *testing.T) {
	require.NoError(t, sleepCtx(context.Background(), 0))
	require.NoError(t, sleepCtx(context.Background(), time.Nanosecond))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, sleepCtx(ctx, time.Hour), context.Canceled)
}
