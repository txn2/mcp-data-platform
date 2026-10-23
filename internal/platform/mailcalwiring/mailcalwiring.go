// Package mailcalwiring attaches the platform's dependencies to the caldav and
// imap toolkits (#2246, #2247): the connection store a connection saved on
// another replica is read from, and the resource library mail_attachment
// saves into. It holds what pkg/platform would otherwise grow by, which is how
// the facade stays within its size budget.
package mailcalwiring

import (
	"github.com/txn2/mcp-data-platform/internal/platform/connrecords"
	"github.com/txn2/mcp-data-platform/internal/platform/resourcewrite"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	caldavkit "github.com/txn2/mcp-data-platform/pkg/toolkits/caldav"
	imapkit "github.com/txn2/mcp-data-platform/pkg/toolkits/imap"
)

// Saved reads one kind's saved connections; nil without a database.
type Saved func(kind string, kindNotFound error) connrecords.Reader

// Wire attaches both dependencies to every live caldav and imap toolkit. It
// runs before the toolkits' tools are registered: an imap toolkit registers
// mail_attachment only when its library is already set, and Start registers
// tools before the runtime wiring runs (#1675). A nil library leaves
// mail_attachment unregistered.
func Wire(reg *registry.Registry, library *resourcewrite.Ref, saved Saved) {
	if reg == nil {
		return
	}
	for _, tk := range reg.GetByKind(caldavkit.Kind) {
		if c, ok := tk.(*caldavkit.Toolkit); ok {
			c.SetConnectionStore(saved(caldavkit.Kind, caldavkit.ErrConnectionNotFound))
		}
	}
	for _, tk := range reg.GetByKind(imapkit.Kind) {
		m, ok := tk.(*imapkit.Toolkit)
		if !ok {
			continue
		}
		m.SetConnectionStore(saved(imapkit.Kind, imapkit.ErrConnectionNotFound))
		if library != nil {
			m.SetResourceLander(library)
		}
	}
}
