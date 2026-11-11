package bucketsource

import (
	"context"
	"fmt"

	s3client "github.com/txn2/mcp-s3/pkg/client"

	"github.com/txn2/mcp-data-platform/pkg/persona"
	"github.com/txn2/mcp-data-platform/pkg/registry"
	s3kit "github.com/txn2/mcp-data-platform/pkg/toolkits/s3"
)

// Client is what a reader of one bucket uses: s3_list's listing, and a read
// of a byte range, so an object is never held whole past its limit.
type Client interface {
	ListObjects(ctx context.Context, bucket, prefix, delimiter string, maxKeys int32, continueToken string) (*s3client.ListObjectsOutput, error)
	GetObjectRange(ctx context.Context, bucket, key string, offset, length int64) (*s3client.ObjectContent, error)
}

// Clients finds the client reading a bucket on a connection, refusing an
// unknown connection or a bucket outside the connection's bucket_prefix.
type Clients interface {
	ReadClient(connection, bucket string) (Client, error)
}

// FromToolkits is the access a deployment grants: a connection's reading
// client from its S3 toolkit, read on every open so a connection added at run
// time is the one used, and the persona connection and tool rules from the
// live registry. No rule is restated here: ReadClient is the S3 toolkit's own
// bucket_prefix check, and Allowed and ToolAllowed are the persona filter a
// tool call is authorized under.
func FromToolkits(reg *registry.Registry, personas *persona.Registry) *Access {
	a := &Access{Clients: registryClients{reg: reg}}
	if personas != nil {
		a.Allowed = func(name, connection string) bool {
			p, ok := personas.Get(name)
			return ok && (&persona.ToolFilter{}).IsConnectionAllowed(p, connection)
		}
		a.ToolAllowed = personas.ToolAllowed
	}
	return a
}

// registryClients finds a connection in the deployment's S3 toolkits.
type registryClients struct{ reg *registry.Registry }

// ReadClient is the reading client of the S3 toolkit holding the connection.
func (r registryClients) ReadClient(connection, bucket string) (Client, error) {
	if r.reg != nil {
		for _, tk := range r.reg.GetByKind("s3") {
			s3tk, ok := tk.(*s3kit.Toolkit)
			if !ok || !s3tk.HasConnection(connection) {
				continue
			}
			c, err := s3tk.ReadClient(connection, bucket)
			if err != nil {
				return nil, err //nolint:wrapcheck // the toolkit's own sentence
			}
			ranged, ok := c.(Client)
			if !ok {
				return nil, fmt.Errorf("connection %q cannot read an object by range", connection)
			}
			return ranged, nil
		}
	}
	return nil, fmt.Errorf("no S3 connection named %q", connection)
}
