// Package whsetup answers the checks a webhook sender makes of an address
// before it delivers to it (#2223): Slack's url_verification and monday.com's
// challenge echoed back, Zoom's endpoint.url_validation answered with an HMAC
// of its token, Discord's PING answered with a PONG, and Microsoft Graph's
// validation token returned as plain text.
//
// It decides whether a request is a source's setup check and what the answer
// is. It writes nothing and counts nothing: the receiver writes the answer,
// counts it as a setup check, and stores nothing for it.
package whsetup

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"

	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/wirejson"
)

// Content types an answer is written in.
const (
	ContentJSON = "application/json"
	// ContentText is Graph's validation answer, which must be plain text.
	ContentText = "text/plain; charset=utf-8"
)

// Answer is the body a setup check is answered 200 with.
type Answer struct {
	ContentType string
	Body        []byte
}

// graphValidationParam is the query parameter Microsoft Graph sends its
// validation token in, on a subscription's notification URL and its
// lifecycle notification URL alike.
const graphValidationParam = "validationToken"

// slackVerificationType is the type Slack's url_verification check carries.
const slackVerificationType = "url_verification"

// Fields a challenge_echo check may carry beside its challenge: Slack sends a
// token and the url_verification type with it, monday.com nothing.
const (
	fieldToken = "token"
	fieldType  = "type"
)

// zoomValidationEvent is the event Zoom validates an endpoint with.
const zoomValidationEvent = "endpoint.url_validation"

// discordPing is the interaction type of Discord's PING.
const discordPing = 1

// FromQuery answers a graph_validation source's validation request, which is
// read from the address alone: the token, URL-decoded, as plain text. It is
// answered before the body is read or verified, since Graph's validation
// carries no clientState, and is never written into an HTML or JSON body.
func FromQuery(src whsource.Source, query url.Values) (Answer, bool) {
	if src.Config.Handshake != whsource.HandshakeGraphValidation {
		return Answer{}, false
	}
	token := query.Get(graphValidationParam)
	if token == "" {
		return Answer{}, false
	}
	return Answer{ContentType: ContentText, Body: []byte(token)}, true
}

// BeforeVerification answers a challenge_echo source's check with its
// challenge. monday.com does not sign its challenge, so it is answered before
// the request is verified; echoing it hands the caller back only what it sent.
func BeforeVerification(src whsource.Source, body []byte) (Answer, bool) {
	if src.Config.Handshake != whsource.HandshakeChallengeEcho {
		return Answer{}, false
	}
	field := src.Config.Challenge()
	challenge, ok := challengeOf(body, field)
	if !ok {
		return Answer{}, false
	}
	return jsonAnswer(map[string]any{field: challenge})
}

// challengeOf reads a challenge_echo check: a JSON object whose challenge
// field is a non-empty string, which carries nothing else but a token and a
// url_verification type. An event that happens to have a challenge field
// carries other fields, and is not a check.
func challengeOf(body []byte, field string) (string, bool) {
	var doc map[string]json.RawMessage
	if json.Unmarshal(body, &doc) != nil {
		return "", false
	}
	var challenge string
	if json.Unmarshal(doc[field], &challenge) != nil || challenge == "" {
		return "", false
	}
	for k, v := range doc {
		switch k {
		case field, fieldToken:
		case fieldType:
			var t string
			if json.Unmarshal(v, &t) != nil || t != slackVerificationType {
				return "", false
			}
		default:
			return "", false
		}
	}
	return challenge, true
}

// AfterVerification answers the checks made of a request that has verified:
// Zoom's url_validation, whose answer is an HMAC under the source's secret
// (answered unverified, it would sign any text for anyone, which forges Zoom
// signatures), and Discord's PING, which Discord expects refused unsigned.
func AfterVerification(src whsource.Source, body []byte) (Answer, bool) {
	switch src.Config.Handshake {
	case whsource.HandshakeZoom:
		var check struct {
			Event   string `json:"event"`
			Payload struct {
				PlainToken string `json:"plainToken"`
			} `json:"payload"`
		}
		if json.Unmarshal(body, &check) != nil || check.Event != zoomValidationEvent || check.Payload.PlainToken == "" {
			return Answer{}, false
		}
		return jsonAnswer(map[string]any{
			"plainToken":     check.Payload.PlainToken,
			"encryptedToken": zoomEncryptedToken(src.Auth.Secret, check.Payload.PlainToken),
		})
	case whsource.HandshakeDiscord:
		var check struct {
			Type int `json:"type"`
		}
		if json.Unmarshal(body, &check) != nil || check.Type != discordPing {
			return Answer{}, false
		}
		return jsonAnswer(map[string]any{"type": discordPing})
	}
	return Answer{}, false
}

// zoomEncryptedToken is Zoom's encryptedToken: the hex HMAC-SHA256 of the
// plain token under the secret token.
func zoomEncryptedToken(secret, plain string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(plain))
	return hex.EncodeToString(mac.Sum(nil))
}

// jsonAnswer is a JSON answer of v.
func jsonAnswer(v map[string]any) (Answer, bool) {
	b, err := wirejson.Marshal(v)
	if err != nil {
		return Answer{}, false
	}
	return Answer{ContentType: ContentJSON, Body: b}, true
}
