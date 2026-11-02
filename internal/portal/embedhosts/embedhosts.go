// Package embedhosts is the list of origins an HTML asset may frame and load
// media from (#2069): a video player (Wistia, a deployment's own) or the CDN a
// clip is served from. The share viewer's Content-Security-Policy and the
// portal's asset frame both read it, so a clip that plays in the portal plays
// on a share link, and one removed from the list is blocked on both.
//
// The list is written into a policy, so each entry is held to the one form a
// policy source takes for a host: https, a host name or a leading `*.` label,
// an optional port, and nothing else. Anything that could end a directive or
// widen the policy is refused by name.
package embedhosts

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"strconv"
	"strings"
)

// SettingsSection is the platform_settings section an administrator's list
// is stored under.
const SettingsSection = "portal_embeds"

// MaxHosts bounds the list, so the policy header it is written into stays a
// header.
const MaxHosts = 50

// Default is the list a deployment that sets none gets: the Wistia player,
// which plays inside the asset frame, and the YouTube and Vimeo players,
// which do not run in its opaque origin and draw as a link card instead
// (#2069, #2396). Listing them keeps a card's poster and an embed that a
// later frame can play under one setting.
var Default = []string{
	"https://fast.wistia.net",
	"https://fast.wistia.com",
	"https://www.youtube-nocookie.com",
	"https://www.youtube.com",
	"https://player.vimeo.com",
}

// Defaults returns a copy of Default, so a caller cannot change it.
func Defaults() []string {
	return append([]string(nil), Default...)
}

// label is one DNS label: letters, digits and inner hyphens.
var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ErrInvalid is what every refusal of an entry is (errors.Is).
var ErrInvalid = errors.New("invalid embed host")

// InvalidError is the refusal of one entry, or of the list when Entry is
// empty, naming what is wrong with it.
type InvalidError struct {
	Entry  string
	Reason string
}

func (e *InvalidError) Error() string {
	if e.Entry == "" {
		return "embed hosts: " + e.Reason
	}
	return fmt.Sprintf("embed host %q: %s", e.Entry, e.Reason)
}

// Is reports that the refusal is an ErrInvalid.
func (*InvalidError) Is(target error) bool { return target == ErrInvalid }

const (
	maxPort     = 65535
	maxHostName = 253
)

// Normalize returns the list in its stored form, each entry
// `https://host[:port]`, lower case, without duplicates and in the order
// given. A bare host gains the https scheme. An empty list is valid and turns
// embeds off.
func Normalize(entries []string) ([]string, error) {
	if len(entries) > MaxHosts {
		return nil, &InvalidError{Reason: fmt.Sprintf("at most %d hosts", MaxHosts)}
	}
	out := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for _, raw := range entries {
		host, err := normalizeOne(raw)
		if err != nil {
			return nil, err
		}
		if !seen[host] {
			seen[host] = true
			out = append(out, host)
		}
	}
	return out, nil
}

// normalizeOne holds one entry to the policy-source form.
func normalizeOne(raw string) (string, error) {
	entry := strings.ToLower(strings.TrimSpace(raw))
	rest, hadScheme := strings.CutPrefix(entry, "https://")
	if !hadScheme && strings.Contains(entry, "://") {
		return "", &InvalidError{Entry: raw, Reason: "only https hosts can be embedded"}
	}
	rest = strings.TrimSuffix(rest, "/")
	host, port, err := splitPort(rest)
	if err == nil {
		err = checkHost(host)
	}
	if err != nil {
		return "", &InvalidError{Entry: raw, Reason: err.Error()}
	}
	if port != "" {
		host += ":" + port
	}
	return "https://" + host, nil
}

// splitPort separates an optional numeric port.
func splitPort(s string) (host, port string, err error) {
	if !strings.Contains(s, ":") {
		return s, "", nil
	}
	h, p, err := net.SplitHostPort(s)
	if err != nil {
		return "", "", errors.New("give a host name, with a port if it needs one")
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > maxPort {
		return "", "", errors.New("the port is not a number from 1 to 65535")
	}
	return h, p, nil
}

// checkHost accepts a dotted host name, optionally beginning `*.`, and refuses
// everything else: a path, a query, a bare `*`, whitespace, quotes, `;` and
// `,`, any of which would end the source or the directive it is written into.
func checkHost(host string) error {
	name, _ := strings.CutPrefix(host, "*.")
	labels := strings.Split(name, ".")
	if len(labels) < 2 || len(host) > maxHostName {
		return errors.New("give a host name such as player.example.com")
	}
	for _, l := range labels {
		if !label.MatchString(l) {
			return errors.New("give a host name such as player.example.com, with no path, spaces or punctuation")
		}
	}
	return nil
}

// Settings is the stored section.
type Settings struct {
	// Hosts are the origins, in their stored form.
	Hosts []string `json:"hosts"`
}

// Validate holds the stored list to the policy-source form. A value read back
// that fails it is not used.
func (s Settings) Validate() error {
	_, err := Normalize(s.Hosts)
	return err
}

// Config is the `portal.embeds` section of the configuration file. Hosts nil
// takes Default; an empty list turns embeds off. An administrator's list,
// once written, is used instead.
type Config struct {
	Hosts *[]string `yaml:"hosts"`
}

// Validate refuses a configured list an administrator could not save.
func (c Config) Validate() error {
	if c.Hosts == nil {
		return nil
	}
	if _, err := Normalize(*c.Hosts); err != nil {
		return fmt.Errorf("portal.embeds.hosts: %w", err)
	}
	return nil
}

// Configured returns the configured list in its stored form, or Default when
// none is configured. An invalid list, which Validate refused at startup,
// returns Default.
func (c Config) Configured() []string {
	if c.Hosts == nil {
		return Defaults()
	}
	hosts, err := Normalize(*c.Hosts)
	if err != nil {
		return Defaults()
	}
	return hosts
}

// From is the list a reader function gives, the default when there is no
// reader, and never nil, so a list turned off reads as [].
func From(ctx context.Context, read func(context.Context) []string) []string {
	if read == nil {
		return Defaults()
	}
	if hosts := read(ctx); hosts != nil {
		return hosts
	}
	return []string{}
}

// Sources renders the list as the space-separated sources a directive takes,
// "" for an empty list. Every entry is normalized again, so a value that did
// not pass through Normalize cannot reach a header.
func Sources(hosts []string) string {
	clean, err := Normalize(hosts)
	if err != nil {
		return ""
	}
	return strings.Join(clean, " ")
}
