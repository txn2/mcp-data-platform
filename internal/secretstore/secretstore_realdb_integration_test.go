//go:build integration

package secretstore

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/secretref"
	"github.com/txn2/mcp-data-platform/internal/testdb"
	"github.com/txn2/mcp-data-platform/internal/totp"
)

// TestSecretsRealDB runs a secret through its life against a migrated
// database: created with its value encrypted, rescoped without retyping the
// value, rotated, looked up in and out of scope, and deleted.
func TestSecretsRealDB(t *testing.T) {
	db := testdb.New(t)
	s := NewStore(db, prefixEncryptor{})
	ctx := context.Background()

	value := "hunter22"
	sec, created, err := s.Put(ctx, Write{Name: "portal_password", Description: "vendor portal", Value: &value, AllowConnections: []string{"grid"}, Actor: "admin@example.com"})
	require.NoError(t, err)
	assert.True(t, created)
	assert.Equal(t, []string{}, sec.AllowPersonas)

	var stored string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value FROM gateway_secrets WHERE name = 'portal_password'`).Scan(&stored))
	assert.Equal(t, "enc:hunter22", stored, "the value is encrypted at rest")

	_, created, err = s.Put(ctx, Write{Name: "portal_password", Description: "rescoped", AllowConnections: []string{"grid", "vendor"}, AllowPersonas: []string{"admin"}, Actor: "other@example.com"})
	require.NoError(t, err)
	assert.False(t, created)
	got, err := s.Lookup(ctx, "vendor", "admin")("portal_password")
	require.NoError(t, err)
	assert.Equal(t, "hunter22", got, "an update without a value keeps the stored one")

	rotated := "correct-horse"
	_, _, err = s.Put(ctx, Write{Name: "portal_password", AllowConnections: []string{"grid"}, Value: &rotated})
	require.NoError(t, err)
	got, err = s.Lookup(ctx, "grid", "analyst")("portal_password")
	require.NoError(t, err, "personas cleared by the update")
	assert.Equal(t, "correct-horse", got, "a rotated value is read on the next call")

	_, err = s.Lookup(ctx, "vendor", "")("portal_password")
	require.ErrorContains(t, err, "may not be sent through connection")

	list, err := s.List(ctx)
	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.Equal(t, "admin@example.com", list[0].CreatedBy)
	assert.Equal(t, "", list[0].UpdatedBy)

	require.NoError(t, s.Delete(ctx, "portal_password"))
	require.ErrorIs(t, s.Delete(ctx, "portal_password"), ErrNotFound)
	_, err = s.Get(ctx, "portal_password")
	require.ErrorIs(t, err, ErrNotFound)
}

// TestAuthenticatorSeedRealDB runs a totp secret against a migrated
// database (#2065): saved from an otpauth URI with its parameters, the seed
// stored in canonical base32 and encrypted, a code issued once per period
// through the row every replica shares, a rescope that keeps the kind, and a
// new seed that starts its own count.
func TestAuthenticatorSeedRealDB(t *testing.T) {
	db := testdb.New(t)
	clock := &fakeClock{t: time.Unix(1_111_111_111, 0)}
	s := NewStore(db, prefixEncryptor{}).WithClock(clock.now, clock.sleep)
	ctx := context.Background()

	uri := "otpauth://totp/Vendor:ops?secret=" + strings.ToLower(rfcSeed) + "&algorithm=SHA256&digits=8&period=60"
	sec, _, err := s.Put(ctx, Write{Name: "vendor_mfa", Kind: KindTOTP, Value: &uri, AllowConnections: []string{"grid"}})
	require.NoError(t, err)
	assert.Equal(t, KindTOTP, sec.Kind)
	assert.Equal(t, &totp.Params{Algorithm: "SHA256", Digits: 8, Period: 60}, sec.TOTP)
	var stored string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT value FROM gateway_secrets WHERE name = 'vendor_mfa'`).Scan(&stored))
	assert.Equal(t, "enc:"+rfcSeed, stored)

	p := *sec.TOTP
	first, err := s.Lookup(ctx, "grid", "")(secretref.TOTPName("vendor_mfa"))
	require.NoError(t, err)
	assert.Equal(t, rfcCode(t, p, p.Counter(time.Unix(1_111_111_111, 0))), first)
	second, err := s.Lookup(ctx, "grid", "")(secretref.TOTPName("vendor_mfa"))
	require.NoError(t, err)
	assert.NotEqual(t, first, second, "the second fill in a period waited for the next")
	// A third call arriving in the first period, while the second waits,
	// finds this period and the next both issued.
	clock.t = time.Unix(1_111_111_111, 0)
	_, err = s.Lookup(ctx, "grid", "")(secretref.TOTPName("vendor_mfa"))
	require.ErrorContains(t, err, "try again after")

	_, _, err = s.Put(ctx, Write{Name: "vendor_mfa", Description: "rescoped", AllowConnections: []string{"grid", "vendor"}})
	require.NoError(t, err)
	got, err := s.Get(ctx, "vendor_mfa")
	require.NoError(t, err)
	assert.Equal(t, KindTOTP, got.Kind, "a rescope keeps the kind")

	// A new seed starts its own count: the period claimed under the old one
	// does not hold it back.
	bare := rfcSeed
	_, _, err = s.Put(ctx, Write{Name: "vendor_mfa", Kind: KindTOTP, Value: &bare, AllowConnections: []string{"grid"}})
	require.NoError(t, err)
	var last int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT totp_last_period FROM gateway_secrets WHERE name = 'vendor_mfa'`).Scan(&last))
	assert.Zero(t, last)
	current, err := s.Code(ctx, "vendor_mfa")
	require.NoError(t, err)
	assert.Len(t, current.Code, 6)
}
