package whimap

import (
	"context"
	"fmt"
	"strings"

	"github.com/txn2/mcp-data-platform/internal/imapmail"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
)

// Mailbox is what the admin service asks of an imap source: whether its
// mailbox can be read as configured, and how each folder's polls are going.
type Mailbox struct {
	marks   *Marks
	repairs *Repairs
	open    func(ctx context.Context, a imapmail.Account) (*imapmail.Session, error)
}

// NewMailbox returns the admin side of the imap input.
func NewMailbox(marks *Marks, repairs *Repairs) *Mailbox {
	return &Mailbox{marks: marks, repairs: repairs, open: imapmail.Open}
}

// Repair is what the source's attachment repair did, nil until it has run
// (#2836).
func (m *Mailbox) Repair(ctx context.Context, source string) (*RepairRecord, error) {
	if m.repairs == nil {
		return nil, nil //nolint:nilnil // no store, no repair
	}
	return m.repairs.Get(ctx, source)
}

// Check logs in and opens each folder read-only, so a source whose login or
// folders are wrong is refused when it is saved rather than failing every poll.
func (m *Mailbox) Check(ctx context.Context, src whsource.Source) error {
	s, err := m.open(ctx, account(src))
	if err != nil {
		return fmt.Errorf("the mailbox did not accept the source's login: %w", err)
	}
	defer s.Close() //nolint:errcheck // logout after the check
	var missing []string
	for _, f := range src.Config.IMAP.Folders {
		if _, _, err := s.Examine(f); err != nil {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the mailbox has no folder %s", strings.Join(missing, ", "))
	}
	return nil
}

// Marks lists a source's folders as its polls left them.
func (m *Mailbox) Marks(ctx context.Context, source string) ([]Mark, error) {
	if m.marks == nil {
		return []Mark{}, nil
	}
	return m.marks.List(ctx, source)
}
