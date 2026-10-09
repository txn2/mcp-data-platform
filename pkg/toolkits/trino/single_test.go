package trino

import (
	trinoclient "github.com/txn2/mcp-trino/pkg/client"
	"github.com/txn2/mcp-trino/pkg/multiserver"
)

// singleClient is a manager over one client, which is how a test hands the
// toolkit a sqlmock-backed client: NewMulti is the only constructor, and every
// statement resolves its client through the manager.
func singleClient(c *trinoclient.Client) *multiserver.Manager {
	return multiserver.SingleClientManager(c, trinoclient.Config{})
}

// newSingle builds a toolkit with one connection, through the constructor the
// platform uses.
func newSingle(name string, cfg Config) (*Toolkit, error) {
	return NewMulti(MultiConfig{DefaultConnection: name, Instances: map[string]Config{name: cfg}})
}
