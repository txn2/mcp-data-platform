package smtp

import (
	"slices"
	"testing"
)

func TestSettingsInput_Validate(t *testing.T) {
	tests := []struct {
		name    string
		in      SettingsInput
		wantErr bool
	}{
		{name: "valid enabled", in: SettingsInput{Enabled: true, Host: "h", Port: 587, From: "p@example.com"}},
		{name: "disable only defaults", in: SettingsInput{Enabled: false}},
		{name: "bad tls", in: SettingsInput{TLSMode: "ssl3"}, wantErr: true},
		{name: "port too high", in: SettingsInput{Port: 70000}, wantErr: true},
		{name: "negative port", in: SettingsInput{Port: -1}, wantErr: true},
		{name: "enabled no host", in: SettingsInput{Enabled: true, Port: 587}, wantErr: true},
		{name: "enabled bad from", in: SettingsInput{Enabled: true, Host: "h", From: "nope"}, wantErr: true},
		// Port 465 speaks implicit TLS only; the other two modes open plaintext
		// and stall until the send timeout instead of failing fast.
		{
			name:    "465 with starttls",
			in:      SettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com", TLSMode: TLSModeStartTLS},
			wantErr: true,
		},
		{
			name:    "465 with none",
			in:      SettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com", TLSMode: TLSModeNone},
			wantErr: true,
		},
		{
			// TLSMode defaults to starttls, so an omitted mode on 465 is the
			// same broken pairing and must not slip through the default.
			name:    "465 with defaulted mode",
			in:      SettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com"},
			wantErr: true,
		},
		{
			name: "465 with implicit",
			in:   SettingsInput{Enabled: true, Host: "h", Port: 465, From: "p@example.com", TLSMode: TLSModeImplicit},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			msg := tc.in.Validate()
			if tc.wantErr && msg == "" {
				t.Error("expected validation message")
			}
			if !tc.wantErr && msg != "" {
				t.Errorf("unexpected validation message: %q", msg)
			}
		})
	}
}

func TestSettingsView(t *testing.T) {
	s := Settings{
		Enabled: true, Host: "h", Port: 587, Username: "u",
		Password: "secret", From: "f@example.com", FromName: "F",
		TLSMode: TLSModeStartTLS, UpdatedBy: "a@b.io",
	}
	v := s.View()
	if !v.PasswordSet {
		t.Error("password_set must reflect a stored password")
	}
	if v.Host != "h" || v.Username != "u" || v.UpdatedBy != "a@b.io" {
		t.Errorf("view mapping wrong: %+v", v)
	}
	s.Password = ""
	if s.View().PasswordSet {
		t.Error("password_set must be false without a stored password")
	}

	u := UnconfiguredView()
	if u.Port != 587 || u.TLSMode != TLSModeStartTLS || u.Enabled {
		t.Errorf("unconfigured view wrong: %+v", u)
	}
}

// TestSettingsView_PlaintextAuthWarning covers #1072: TLSModeNone with a
// credential is accepted but hazardous, and the view is what carries the
// hazard to the operator.
func TestSettingsView_PlaintextAuthWarning(t *testing.T) {
	tests := []struct {
		name     string
		settings Settings
		want     bool
	}{
		{"none with password", Settings{TLSMode: TLSModeNone, Password: "p"}, true},
		{"none with username", Settings{TLSMode: TLSModeNone, Username: "u"}, true},
		{"none without credentials", Settings{TLSMode: TLSModeNone}, false},
		{"starttls with credentials", Settings{TLSMode: TLSModeStartTLS, Username: "u", Password: "p"}, false},
		{"implicit with credentials", Settings{TLSMode: TLSModeImplicit, Username: "u", Password: "p"}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			warnings := tc.settings.View().Warnings
			got := slices.Contains(warnings, PlaintextAuthWarning)
			if got != tc.want {
				t.Errorf("plaintext warning = %v; want %v (warnings: %v)", got, tc.want, warnings)
			}
		})
	}
}

func TestTestEmailRequest_Validate(t *testing.T) {
	ok := TestEmailRequest{To: "a@b.io"}
	if msg := ok.Validate(); msg != "" {
		t.Errorf("valid recipient rejected: %s", msg)
	}
	bad := TestEmailRequest{To: "not an address"}
	if msg := bad.Validate(); msg == "" {
		t.Error("invalid recipient accepted")
	}
}

func TestSettingsInput_SettingsMapping(t *testing.T) {
	in := SettingsInput{
		Enabled: true, Host: "h", Username: "u", Password: "p",
		From: "f@example.com", FromName: "F",
	}
	if msg := in.Validate(); msg != "" {
		t.Fatalf("Validate: %s", msg)
	}
	s := in.Settings()
	if s.Port != 587 || s.TLSMode != TLSModeStartTLS {
		t.Errorf("defaults not applied: %+v", s)
	}
	if s.Host != "h" || s.Password != "p" || s.From != "f@example.com" {
		t.Errorf("mapping wrong: %+v", s)
	}
}
