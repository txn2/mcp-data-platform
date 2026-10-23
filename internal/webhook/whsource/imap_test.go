package whsource

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func imapSource() Source {
	return Source{
		Name: "vendor-mail", Connection: "scratch",
		Auth: Auth{Mode: AuthIMAP, Username: "platform", Secret: "pw"},
		Config: Config{Input: InputIMAP, IMAP: &IMAP{
			Host: "imap.example.com", Folders: []string{"Platform"}, AllowSenders: []string{"@vendor.example"},
			TrustedAuthServID: "mx.example.com",
		}},
	}
}

func TestIMAP_Defaults(t *testing.T) {
	s := imapSource().WithDefaults()
	assert.True(t, s.IsIMAP())
	assert.Equal(t, IMAPTLS, s.Config.IMAP.TLS)
	assert.Equal(t, 993, s.Config.IMAP.Port)
	assert.Equal(t, DefaultIMAPPollSeconds, s.Config.IMAP.PollSeconds)
	assert.True(t, s.Config.IMAP.AuthRequired())
	require.NoError(t, Validate(s))

	st := imapSource()
	st.Config.IMAP.TLS = IMAPStartTLS
	assert.Equal(t, 143, st.WithDefaults().Config.IMAP.Port)
	assert.Equal(t, InputHTTP, Source{}.WithDefaults().Config.Input)
	assert.False(t, Source{}.WithDefaults().IsIMAP())
}

func TestIMAP_Refusals(t *testing.T) {
	off := false
	cases := map[string]func(s *Source){
		"auth.secret is required":           func(s *Source) { s.Auth.Secret = "" },
		"config.imap is required":           func(s *Source) { s.Config.IMAP = nil },
		"mode imap with the mailbox's":      func(s *Source) { s.Auth.Username = "" },
		"config.imap.host is required":      func(s *Source) { s.Config.IMAP.Host = " " },
		"plain IMAP":                        func(s *Source) { s.Config.IMAP.TLS = "none" },
		"is not a port":                     func(s *Source) { s.Config.IMAP.Port = 70000 },
		"poll_seconds must be between":      func(s *Source) { s.Config.IMAP.PollSeconds = 5 },
		"must be positive":                  func(s *Source) { s.Config.IMAP.MaxMessageBytes = -1 },
		"holds no PEM certificate":          func(s *Source) { s.Config.IMAP.TLSCABundlePEM = "nope" },
		"folders is required":               func(s *Source) { s.Config.IMAP.Folders = nil },
		"allow_senders is required":         func(s *Source) { s.Config.IMAP.AllowSenders = nil },
		"trusted_authserv_id is required":   func(s *Source) { s.Config.IMAP.TrustedAuthServID = "" },
		"neither an address nor an @domain": func(s *Source) { s.Config.IMAP.AllowSenders = []string{"vendor.example"} },
		"config.input must be http or imap": func(s *Source) { s.Config.Input = "ftp" },
	}
	for want, mutate := range cases {
		s := imapSource().WithDefaults()
		mutate(&s)
		err := Validate(s)
		require.Error(t, err, want)
		assert.True(t, strings.Contains(err.Error(), want), "%s: %v", want, err)
	}
	s := imapSource()
	s.Config.IMAP.RequireAuthPass = &off
	s.Config.IMAP.TrustedAuthServID = ""
	assert.NoError(t, Validate(s.WithDefaults()), "without required authentication no authserv-id is needed")
}
