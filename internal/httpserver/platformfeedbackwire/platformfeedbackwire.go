// Package platformfeedbackwire assembles platform feedback (#2820) from the
// platform: the settings section, the content secret check over the
// deployment's own secret values, the embedder that folds a repeat, and the
// notification channels a post is sent to. It binds the service to
// manage_feedback's topic=platform and mounts the admin routes.
package platformfeedbackwire

import (
	"context"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/platform/notifydelivery"
	"github.com/txn2/mcp-data-platform/internal/platform/settingsjson"
	"github.com/txn2/mcp-data-platform/internal/platformfeedback"
	"github.com/txn2/mcp-data-platform/internal/secretscan"
	"github.com/txn2/mcp-data-platform/internal/secretstore"
	"github.com/txn2/mcp-data-platform/pkg/embedding"
	"github.com/txn2/mcp-data-platform/pkg/platform"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	portaltoolkit "github.com/txn2/mcp-data-platform/pkg/toolkits/portal"
)

// portalKind is the kind of the toolkit that registers manage_feedback.
const portalKind = "portal"

// Service builds platform feedback over the platform, nil without a database.
// notify may be nil, in which case a post is stored and sent nowhere.
func Service(p *platform.Platform, notify *notifydelivery.Handle) *platformfeedback.Service {
	if p.DB() == nil {
		return nil
	}
	d := platformfeedback.Deps{
		DB:       p.DB(),
		Section:  settingsjson.New[platformfeedback.Settings](p.DB(), platformfeedback.SectionName),
		Detector: secretscan.Detector{Known: knownValues(p)},
		ServiceAccount: func(name string) bool {
			if reg := p.PersonaRegistry(); reg != nil {
				if per, ok := reg.Get(name); ok {
					return per.ServiceAccount
				}
			}
			return false
		},
		KnownPersona: func(name string) bool {
			reg := p.PersonaRegistry()
			if reg == nil {
				return false
			}
			_, ok := reg.Get(name)
			return ok
		},
		PortalURL: p.Config().Portal.PublicBaseURL,
	}
	if emb := p.EmbeddingProvider(); embedding.IsConfigured(emb) {
		d.Embed = emb.Embed
	}
	if notify != nil {
		d.Channels, d.Sender = notify.Channels(), notify.Enqueuer()
	}
	return platformfeedback.New(d)
}

// knownValues is the deployment's own secret values for one scan: every
// stored secret, and every connection configuration value the encryption
// layer seals at rest (fieldcrypt's key sets, the one list of what is a
// secret in a connection's configuration).
func knownValues(p *platform.Platform) secretscan.Known {
	return func(ctx context.Context) ([]string, error) {
		values, err := secretstore.NewStore(p.DB(), p.RestEncryptor()).Values(ctx)
		if err != nil {
			return nil, err //nolint:wrapcheck // names the read
		}
		store := p.ConnectionStore()
		if store == nil {
			return values, nil
		}
		conns, err := store.List(ctx)
		if err != nil {
			return nil, err //nolint:wrapcheck // names the read
		}
		for _, c := range conns {
			values = append(values, sealedValues(c.Config)...)
		}
		return values, nil
	}
}

// sealedValues is a connection configuration's values under the keys the
// encryption layer seals, nested maps' inner values included.
func sealedValues(cfg map[string]any) []string {
	var out []string
	for _, k := range fieldcrypt.SensitiveConfigKeyList() {
		if s, ok := cfg[k].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	for _, k := range fieldcrypt.SensitiveNestedMapKeyList() {
		out = append(out, nestedValues(cfg[k])...)
	}
	return out
}

// nestedValues is a nested map's non-empty string values, as a configuration
// decoded from JSON or written in Go holds them.
func nestedValues(v any) []string {
	var out []string
	switch m := v.(type) {
	case map[string]any:
		for _, inner := range m {
			if s, ok := inner.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case map[string]string:
		for _, s := range m {
			if s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// Bind hands the service to every portal toolkit, so manage_feedback's
// topic=platform posts to it.
func Bind(p *platform.Platform, svc *platformfeedback.Service) {
	if svc == nil || p.ToolkitRegistry() == nil {
		return
	}
	for _, tk := range p.ToolkitRegistry().GetByKind(portalKind) {
		if b, ok := tk.(interface {
			SetPlatformFeedback(portaltoolkit.PlatformFeedback)
		}); ok {
			b.SetPlatformFeedback(svc)
		}
	}
}

// Mount registers the admin routes behind adminWrap, when the admin API is on.
func Mount(mux *http.ServeMux, svc *platformfeedback.Service, adminWrap func(http.Handler) http.Handler, author func(*http.Request) string) {
	if svc == nil || adminWrap == nil {
		return
	}
	platformfeedback.Register(mux, adminWrap, svc, author)
}
