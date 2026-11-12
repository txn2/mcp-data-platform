package toolkitcfg

import (
	"context"
	"fmt"

	s3client "github.com/txn2/mcp-s3/pkg/client"
)

// OpenS3 builds the client a platform store writes through on the named S3
// connection: the portal's assets and the managed-resource library. One copy,
// so the two stores cannot read a connection's configuration differently. The
// store reads an object whole, so the client's one deadline covers a read of
// the largest object it serves (#1773).
func OpenS3(toolkits map[string]any, connection string, maxObjectBytes int64) (*s3client.Client, error) {
	s3Cfg := S3Config(toolkits, connection)
	if s3Cfg == nil {
		return nil, fmt.Errorf("s3 connection %q not found in toolkits config", connection)
	}
	c, err := s3client.New(context.Background(), &s3client.Config{
		Region:          s3Cfg.Region,
		Endpoint:        s3Cfg.Endpoint,
		AccessKeyID:     s3Cfg.AccessKeyID,
		SecretAccessKey: s3Cfg.SecretKey,
		Name:            s3Cfg.ConnectionName,
		UsePathStyle:    s3Cfg.UsePathStyle,
		Timeout:         BlobReadTimeout(s3Cfg.Timeout, maxObjectBytes),
	})
	if err != nil {
		return nil, fmt.Errorf("creating s3 client for connection %q: %w", connection, err)
	}
	return c, nil
}
