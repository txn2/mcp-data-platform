package scripttoken

import (
	"slices"
	"strings"
	"sync"

	"github.com/txn2/mcp-data-platform/internal/jsonwalk"
)

// Minted is the tokens one run's mints issued (#2558). A recording keeps a
// mint's answer and a verify's token as RecordedToken (#2361), and a script
// usually hands a token on: the link a sign-in mail carries is written into a
// mail row and sent to a provider. Those calls are recorded too, so each token
// in Minted is replaced wherever a later call's arguments or answer carry it,
// and no recording holds a token that would authenticate. A replay's mint
// answers RecordedToken, so the strings a test's calls build match the ones
// recorded.
type Minted struct {
	mu     sync.Mutex
	tokens []string
}

// Add keeps one token a mint issued. A nil Minted keeps nothing.
func (m *Minted) Add(token string) {
	if m == nil || token == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens = append(m.tokens, token)
}

// Recorded wraps the recorder *record in place so every token the run's
// mints issue is kept out of each call it records, and returns the Minted
// those mints add to. With no recorder it changes nothing and returns nil,
// whose Add keeps nothing.
func Recorded(record *func(tool string, args, out map[string]any, err error)) *Minted {
	if record == nil || *record == nil {
		return nil
	}
	m := &Minted{}
	*record = m.Recorder(*record)
	return m
}

// Recorder is record with every kept token replaced in each call's arguments
// and answer.
func (m *Minted) Recorder(record func(tool string, args, out map[string]any, err error)) func(tool string, args, out map[string]any, err error) {
	return func(tool string, args, out map[string]any, err error) {
		record(tool, m.redact(args), m.redact(out), err)
	}
}

// redact is v with every kept token replaced by RecordedToken, v itself when
// nothing in it changes.
func (m *Minted) redact(v map[string]any) map[string]any {
	m.mu.Lock()
	tokens := slices.Clone(m.tokens)
	m.mu.Unlock()
	if len(tokens) == 0 || v == nil {
		return v
	}
	out, changed := jsonwalk.Strings(v, func(s string) string {
		for _, t := range tokens {
			s = strings.ReplaceAll(s, t, RecordedToken)
		}
		return s
	})
	if !changed {
		return v
	}
	redacted, _ := out.(map[string]any)
	return redacted
}
