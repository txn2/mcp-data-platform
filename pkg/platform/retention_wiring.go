package platform

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/retention"
)

// wireRetention runs the #1904 sweeps with the platform's lifecycle.
func wireRetention(p *Platform) {
	cfg := retention.Config{
		DB: p.db, Bucket: p.config.Portal.S3Bucket, Portal: !isExplicitlyDisabled(p.config.Portal.Enabled), Every: p.config.Retention.Every,
		DeletedDays:  retention.Days(p.config.Portal.DeletedRetentionDays, retention.DefaultDeletedRetentionDays),
		ArchivedDays: retention.Days(p.config.Memory.ArchivedRetentionDays, retention.DefaultArchivedRetentionDays),
		ProducerDays: retention.Days(p.config.Portal.OrphanedProducerRetentionDays, retention.DefaultProducerRetentionDays),
	}
	if client := p.PortalS3Client(); client != nil {
		cfg.Objects = client
	}
	if loop := retention.Assemble(cfg); loop != nil {
		p.lifecycle.OnStart(func(context.Context) error { loop.Start(); return nil })
		p.lifecycle.OnStop(func(context.Context) error { return loop.Close() })
	}
}
