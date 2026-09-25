package whsource

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func validHMAC() Source {
	return Source{
		Name:       "esp-events",
		Enabled:    true,
		Connection: "scratch",
		Auth:       Auth{Mode: AuthHMAC, Secret: "s3cret", SignatureHeader: "X-Signature"},
	}.WithDefaults()
}

func TestWithDefaults(t *testing.T) {
	s := validHMAC()
	c := s.Config
	assert.Equal(t, HandshakeNone, c.Handshake)
	assert.Equal(t, DefaultMaxBodyBytes, c.MaxBodyBytes)
	assert.Equal(t, DefaultFlushMaxEvents, c.FlushMaxEvents)
	assert.Equal(t, time.Second, c.FlushInterval())
	assert.Equal(t, DefaultFlushMaxBytes, c.FlushMaxBytes)
	assert.Equal(t, DefaultBufferLimit, c.BufferLimit)
	assert.Equal(t, 7*24*time.Hour, c.RawRetention())
	assert.Equal(t, time.Hour, c.Window(), "an hour unless the source says otherwise")
	assert.Equal(t, time.Hour, Config{}.Window())
	assert.Equal(t, 5*time.Minute, Config{CompactEveryMinutes: 5}.Window())
	assert.Equal(t, 400*24*time.Hour, c.CompactedRetention())
	assert.Equal(t, AlgorithmSHA256, s.Auth.Algorithm)
	assert.Equal(t, EncodingHex, s.Auth.Encoding)
	assert.Equal(t, SignedBody, s.Auth.Signed)
	assert.Zero(t, s.Auth.ToleranceSeconds, "no timestamp header, no tolerance")

	withTS := Source{Auth: Auth{Mode: AuthHMAC, TimestampHeader: "X-Timestamp"}}.WithDefaults()
	assert.Equal(t, 5*time.Minute, withTS.Auth.Tolerance())

	zero := 0
	forever := Source{Config: Config{CompactedRetentionDays: &zero}}.WithDefaults()
	assert.Zero(t, forever.Config.CompactedRetention(), "zero keeps compacted hours forever")
	assert.Equal(t, 400*24*time.Hour, Config{}.CompactedRetention())
}

func TestTableNames(t *testing.T) {
	s := Source{Name: "crm-contacts"}
	assert.Equal(t, "webhook_crm_contacts", s.TableName())
	assert.Equal(t, "webhook_crm_contacts_raw", s.RawTableName())
	assert.Equal(t, "webhook_crm_contacts_compacted", s.CompactedTableName())
}

func TestRotate(t *testing.T) {
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	a := Auth{Secret: "old"}

	rotated := a.Rotate("new", time.Hour, now)
	assert.Equal(t, "new", rotated.Secret)
	assert.Equal(t, []string{"new", "old"}, rotated.Secrets(now.Add(59*time.Minute)))
	assert.Equal(t, []string{"new"}, rotated.Secrets(now.Add(time.Hour)), "the overlap ends at PreviousUntil")

	immediate := a.Rotate("new", 0, now)
	assert.Equal(t, []string{"new"}, immediate.Secrets(now))
	assert.Empty(t, immediate.PreviousSecret)

	assert.Equal(t, a, a.Rotate("", time.Hour, now), "no new secret keeps the auth")
	assert.Equal(t, a, a.Rotate("old", time.Hour, now), "the same secret is not a rotation")
}

func TestValidateAccepts(t *testing.T) {
	cases := map[string]Source{
		"hmac": validHMAC(),
		"hmac timestamp": func() Source {
			s := validHMAC()
			s.Auth.TimestampHeader = "X-Timestamp"
			s.Auth.Signed = SignedTimestampBody
			s.Auth.Encoding = EncodingBase64
			s.Auth.Algorithm = AlgorithmSHA1
			s.Config.Handshake = HandshakeCloudEvents
			s.Config.Split = "$.events"
			s.Config.EventIDPath = "id"
			return s
		}(),
		"header token": {Name: "a", Connection: "c", Auth: Auth{Mode: AuthHeaderToken, Secret: "t", Header: "X-Token"}},
		"basic":        {Name: "a", Connection: "c", Auth: Auth{Mode: AuthBasic, Secret: "p", Username: "u"}},
		"path token":   {Name: "a", Connection: "c", Auth: Auth{Mode: AuthPathToken, Secret: "t"}},
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, Validate(s.WithDefaults()))
		})
	}
}

func TestValidateRefuses(t *testing.T) {
	mutate := func(f func(*Source)) Source {
		s := validHMAC()
		f(&s)
		return s
	}
	neg := -1
	cases := map[string]Source{
		"uppercase name":       mutate(func(s *Source) { s.Name = "Events" }),
		"underscore name":      mutate(func(s *Source) { s.Name = "a_b" }),
		"reserved name":        mutate(func(s *Source) { s.Name = "new" }),
		"long name":            mutate(func(s *Source) { s.Name = "a" + strings.Repeat("b", 63) }),
		"no connection":        mutate(func(s *Source) { s.Connection = " " }),
		"no secret":            mutate(func(s *Source) { s.Auth.Secret = "" }),
		"unknown mode":         mutate(func(s *Source) { s.Auth.Mode = "oauth" }),
		"bad algorithm":        mutate(func(s *Source) { s.Auth.Algorithm = "md5" }),
		"no signature header":  mutate(func(s *Source) { s.Auth.SignatureHeader = "" }),
		"bad encoding":         mutate(func(s *Source) { s.Auth.Encoding = "base32" }),
		"bad timestamp header": mutate(func(s *Source) { s.Auth.TimestampHeader = "X Time" }),
		"signed ts without header": mutate(func(s *Source) {
			s.Auth.Signed = SignedTimestampBody
		}),
		"bad signed":         mutate(func(s *Source) { s.Auth.Signed = "headers" }),
		"negative tolerance": mutate(func(s *Source) { s.Auth.ToleranceSeconds = -1 }),
		"token without header": {
			Name: "a", Connection: "c",
			Auth: Auth{Mode: AuthHeaderToken, Secret: "t"},
		},
		"basic colon": {
			Name: "a", Connection: "c",
			Auth: Auth{Mode: AuthBasic, Secret: "p", Username: "u:v"},
		},
		"path token slash": {
			Name: "a", Connection: "c",
			Auth: Auth{Mode: AuthPathToken, Secret: "a/b"},
		},
		"bad handshake":               mutate(func(s *Source) { s.Config.Handshake = "websub" }),
		"bad persona":                 mutate(func(s *Source) { s.Config.Persona = "a/b" }),
		"huge body":                   mutate(func(s *Source) { s.Config.MaxBodyBytes = 1 << 40 }),
		"bad split":                   mutate(func(s *Source) { s.Config.Split = "$[" }),
		"bad key path":                mutate(func(s *Source) { s.Config.KeyPath = "$.." }),
		"zero flush events":           mutate(func(s *Source) { s.Config.FlushMaxEvents = -1 }),
		"long interval":               mutate(func(s *Source) { s.Config.FlushMaxIntervalMS = 120_000 }),
		"huge flush bytes":            mutate(func(s *Source) { s.Config.FlushMaxBytes = 1 << 40 }),
		"huge buffer":                 mutate(func(s *Source) { s.Config.BufferLimit = 2_000_000 }),
		"negative rate":               mutate(func(s *Source) { s.Config.RateLimitPerMinute = -1 }),
		"zero raw retention":          mutate(func(s *Source) { s.Config.RawRetentionDays = -1 }),
		"window not dividing an hour": mutate(func(s *Source) { s.Config.CompactEveryMinutes = 7 }),
		"window over an hour":         mutate(func(s *Source) { s.Config.CompactEveryMinutes = 120 }),
		"negative window":             mutate(func(s *Source) { s.Config.CompactEveryMinutes = -1 }),
		"negative compacted":          mutate(func(s *Source) { s.Config.CompactedRetentionDays = &neg }),
	}
	for name, s := range cases {
		t.Run(name, func(t *testing.T) {
			err := Validate(s.WithDefaults())
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrInvalid), err)
		})
	}
}
