// Package sitestatus holds the rules an environment's status is checked and
// answered by (#2631, #2632): an offline message cleaned of the runes that hide
// or reorder text, a redirect target that is an https URL on a host the
// platform does not answer on, where a redirected request lands, and the
// Retry-After an offline answer carries.
package sitestatus

import (
	"errors"
	"fmt"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/txn2/mcp-data-platform/internal/site"
	"github.com/txn2/mcp-data-platform/internal/untrusted"
)

// Limits on what a status carries.
const (
	// MaxStatusMessage is the most characters an offline message holds.
	MaxStatusMessage = 1000
	// MaxRedirectTarget is the longest a redirect target may be.
	MaxRedirectTarget = 2048
)

// DefaultOfflineMessage is what the offline page says with no message.
const DefaultOfflineMessage = "This site is offline for now."

// ErrStatusInput is a status change whose fields do not fit its status; every
// InputError is one.
var ErrStatusInput = errors.New("status")

// InputError is a refused status field, in the words a person reads.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Is makes every InputError an ErrStatusInput.
func (*InputError) Is(target error) bool { return target == ErrStatusInput }

func inputErr(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

// slash is the path separator a redirected path is cleaned with.
const slash = "/"

// CleanStatusMessage is an offline message as stored: the runes a writer can
// hide or reorder text with removed (untrusted.Clean), line breaks kept, and
// at most MaxStatusMessage characters.
func CleanStatusMessage(raw string) (string, error) {
	msg := strings.TrimSpace(untrusted.Clean(raw))
	if n := utf8.RuneCountInString(msg); n > MaxStatusMessage {
		return "", inputErr("message is %d characters, at most %d", n, MaxStatusMessage)
	}
	return msg, nil
}

// ParseRedirectTarget checks a redirect target (#2632): an absolute https URL
// with a host, no user information, no control character, at most
// MaxRedirectTarget characters, and no host the platform or the environment
// answers on itself (own), which would loop. It returns the target as the
// Location header is written from it.
func ParseRedirectTarget(raw string, own []string) (*url.URL, error) {
	if err := checkTargetText(raw); err != nil {
		return nil, err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, inputErr("redirect_to is not a URL")
	}
	if err := checkTargetURL(u); err != nil {
		return nil, err
	}
	host := site.NormalizeHost(u.Host)
	for _, h := range own {
		if h != "" && (host == h || site.SameOrUnder(host, h)) {
			return nil, inputErr("redirect_to is %s, an address this platform answers on itself, which would send visitors in a loop", host)
		}
	}
	u.Fragment, u.RawFragment = "", ""
	return u, nil
}

// checkTargetText refuses a target that is empty, too long, or holds a
// control character, before it is parsed.
func checkTargetText(raw string) error {
	switch {
	case raw == "":
		return inputErr("redirect_to: the address visitors are sent to, an https:// URL")
	case len(raw) > MaxRedirectTarget:
		return inputErr("redirect_to is %d characters, at most %d", len(raw), MaxRedirectTarget)
	case strings.IndexFunc(raw, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0:
		return inputErr("redirect_to holds a control character, such as a line break")
	}
	return nil
}

// checkTargetURL refuses a parsed target that is not https, carries user
// information or names no host.
func checkTargetURL(u *url.URL) error {
	switch {
	case u.Scheme != "https":
		return inputErr("redirect_to must start with https://, so visitors are never sent to an unencrypted or scripted address")
	case u.User != nil:
		return inputErr("redirect_to may not carry a user name or password")
	case u.Hostname() == "" || u.Opaque != "":
		return inputErr("redirect_to must name a host, as in https://status.example.com")
	}
	return nil
}

// RedirectLocation is where a request to a redirecting environment goes: the
// target, with the request's cleaned path and query appended when keepPath is
// set. The request can only add path below the target: its scheme and host
// are always the target's, whatever the path holds (//evil.example/x lands at
// the target's /evil.example/x).
func RedirectLocation(target *url.URL, keepPath bool, reqPath, rawQuery string) string {
	out := *target
	if !keepPath {
		return out.String()
	}
	clean := path.Clean(slash + strings.TrimLeft(reqPath, slash))
	if strings.HasSuffix(reqPath, slash) && clean != slash {
		clean += slash
	}
	base := strings.TrimSuffix(out.Path, slash)
	out.Path = base + clean
	out.RawPath = ""
	if rawQuery != "" {
		if q, err := url.ParseQuery(rawQuery); err == nil {
			out.RawQuery = q.Encode()
		}
	}
	return out.String()
}

// Stamp fixes an approval of a redirect to the target and path rule it was
// asked for: approving one target never approves another.
func Stamp(s site.EnvironmentStatus) string {
	return "redirect:" + strconv.FormatBool(s.KeepPath) + ":" + s.RedirectTo
}

// RetryAfter is the Retry-After an offline answer carries: the seconds until
// BackAt while it is ahead, "" otherwise.
func RetryAfter(backAt *time.Time, now time.Time) string {
	if backAt == nil || !backAt.After(now) {
		return ""
	}
	secs := int(backAt.Sub(now).Seconds())
	return strconv.Itoa(max(1, secs))
}
