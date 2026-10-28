package whsource

import (
	"github.com/txn2/mcp-data-platform/internal/hmacsig"
)

// Presets a source names its sender by (#2223). Each sets the mode, the
// headers, the signed bytes, the encoding and the setup check that sender
// uses; a setting the source sets itself is kept. The HMAC conventions are
// hmacsig's, the ones the platform's outbound hmac connections sign with.
const (
	PresetStandardWebhooks = hmacsig.PresetStandardWebhooks
	PresetGitHub           = hmacsig.PresetGitHub
	PresetStripe           = hmacsig.PresetStripe
	PresetPlatform         = hmacsig.PresetPlatform
	PresetSlack            = hmacsig.PresetSlack
	PresetZoom             = hmacsig.PresetZoom
	PresetTeams            = hmacsig.PresetTeams
	PresetShopify          = hmacsig.PresetShopify
	PresetLinear           = hmacsig.PresetLinear
	PresetGitLab           = hmacsig.PresetGitLab
	PresetJira             = hmacsig.PresetJira
	PresetMattermost       = "mattermost"
	PresetDiscord          = "discord"
	PresetMonday           = "monday"
	PresetMicrosoftGraph   = "microsoft_graph"
)

// sourcePreset is what a preset fills in.
type sourcePreset struct {
	auth      Auth
	handshake string
}

// linearToleranceSeconds is the window Linear's docs recommend checking its
// webhookTimestamp within.
const linearToleranceSeconds = 60

// sourcePresets are the presets a source can name. The HMAC ones start from
// hmacsig's convention of the same name.
var sourcePresets = map[string]sourcePreset{
	PresetStandardWebhooks: hmacPreset(PresetStandardWebhooks, ""),
	PresetGitHub:           hmacPreset(PresetGitHub, ""),
	PresetStripe:           hmacPreset(PresetStripe, ""),
	PresetPlatform:         hmacPreset(PresetPlatform, ""),
	PresetSlack:            hmacPreset(PresetSlack, HandshakeChallengeEcho),
	PresetZoom:             hmacPreset(PresetZoom, HandshakeZoom),
	PresetTeams:            hmacPreset(PresetTeams, ""),
	PresetShopify:          hmacPreset(PresetShopify, ""),
	PresetLinear:           linearPreset(),
	PresetGitLab:           hmacPreset(PresetGitLab, ""),
	PresetJira:             hmacPreset(PresetJira, ""),
	PresetMattermost:       {auth: Auth{Mode: AuthBodyToken, TokenPath: "token"}},
	PresetDiscord: {
		auth: Auth{
			Mode: AuthEd25519, SignatureHeader: "X-Signature-Ed25519", TimestampHeader: "X-Signature-Timestamp",
			Encoding: EncodingHex, Signed: "{timestamp}{body}",
		},
		handshake: HandshakeDiscord,
	},
	PresetMonday: {auth: Auth{Mode: AuthJWT, Header: DefaultJWTHeader}, handshake: HandshakeChallengeEcho},
	// #nosec G101 -- a path into the body, where the sender's token is, not a credential
	PresetMicrosoftGraph: {auth: Auth{Mode: AuthBodyToken, TokenPath: "value[*].clientState"}, handshake: HandshakeGraphValidation},
}

// hmacPreset is an hmac source configured as hmacsig's preset of that name.
func hmacPreset(name, handshake string) sourcePreset {
	p, _ := hmacsig.LookupPreset(name)
	return sourcePreset{
		auth: Auth{
			Mode: AuthHMAC, Algorithm: p.Algorithm, Encoding: p.Encoding, Prefix: p.Prefix, Signed: p.Signed,
			HeaderFormat: p.Format, HeaderPattern: p.HeaderPattern, SecretEncoding: p.SecretEncoding,
			SignatureHeader: p.SignatureHeader, TimestampHeader: p.TimestampHeader, IDHeader: p.IDHeader,
		},
		handshake: handshake,
	}
}

// linearPreset is Linear: the body signed, and webhookTimestamp, in
// milliseconds, checked within a minute.
func linearPreset() sourcePreset {
	p := hmacPreset(PresetLinear, "")
	p.auth.TimestampPath = "webhookTimestamp"
	p.auth.ToleranceSeconds = linearToleranceSeconds
	return p
}

// PresetNames lists the presets a source can name, in the order the portal
// offers them.
func PresetNames() []string {
	return []string{
		PresetStandardWebhooks, PresetGitHub, PresetStripe, PresetPlatform,
		PresetSlack, PresetZoom, PresetMattermost, PresetTeams, PresetDiscord, PresetMonday,
		PresetShopify, PresetLinear, PresetGitLab, PresetJira, PresetMicrosoftGraph,
	}
}

// KnownPreset reports whether name is a preset a source can name.
func KnownPreset(name string) bool {
	_, ok := sourcePresets[name]
	return ok
}

// withPreset fills the auth settings and the setup check the source leaves
// unset from its preset. The preset's mode replaces the source's: a preset
// names one way of proving a request.
func (s Source) withPreset() Source {
	p, ok := sourcePresets[s.Auth.Preset]
	if !ok {
		return s
	}
	a, d := s.Auth, p.auth
	a.Mode = d.Mode
	for _, f := range []struct {
		dst *string
		def string
	}{
		{&a.Algorithm, d.Algorithm},
		{&a.Encoding, d.Encoding},
		{&a.Prefix, d.Prefix},
		{&a.Signed, d.Signed},
		{&a.HeaderFormat, d.HeaderFormat},
		{&a.HeaderPattern, d.HeaderPattern},
		{&a.SecretEncoding, d.SecretEncoding},
		{&a.SignatureHeader, d.SignatureHeader},
		{&a.TimestampHeader, d.TimestampHeader},
		{&a.IDHeader, d.IDHeader},
		{&a.TimestampPath, d.TimestampPath},
		{&a.TokenPath, d.TokenPath},
		{&a.Header, d.Header},
	} {
		if *f.dst == "" {
			*f.dst = f.def
		}
	}
	if a.ToleranceSeconds == 0 {
		a.ToleranceSeconds = d.ToleranceSeconds
	}
	s.Auth = a
	if s.Config.Handshake == "" || s.Config.Handshake == HandshakeNone {
		if p.handshake != "" {
			s.Config.Handshake = p.handshake
		}
	}
	return s
}
