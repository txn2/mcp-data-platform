package observability

import (
	"context"
	"log/slog"
	"os"
	"sync"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/txn2/mcp-data-platform/internal/buildinfo"
)

// Resource attribute keys. Written directly rather than through a pinned
// semconv module so the package is not coupled to one semantic-conventions
// release; the names are from semantic conventions 1.44.0 (#1893).
// deployment.environment.name is the Stable key that replaced
// deployment.environment; vcs.ref.head.revision is the Release Candidate key
// that replaced vcs.repository.ref.revision. mcp_platform.deployment.id is the
// platform's own: the stable name of one deployment across its replicas and
// restarts, which service.instance.id is not.
const (
	attrServiceName           = "service.name"
	attrServiceVersion        = "service.version"
	attrServiceInstanceID     = "service.instance.id"
	attrDeploymentEnvironment = "deployment.environment.name"
	attrVCSRevision           = "vcs.ref.head.revision"
	attrDeploymentID          = "mcp_platform.deployment.id"
)

// The platform's own resource variables. Each wins over the same key in
// OTEL_RESOURCE_ATTRIBUTES, as OTEL_SERVICE_NAME wins over service.name there.
const (
	// envDeploymentEnvironment sets deployment.environment.name.
	envDeploymentEnvironment = "MCP_PLATFORM_DEPLOYMENT_ENVIRONMENT"

	// envDeploymentID sets mcp_platform.deployment.id: the name a fleet
	// backend tells this deployment's signals apart by. Startup warns when an
	// OTLP exporter is on and it is unset.
	envDeploymentID = "MCP_PLATFORM_DEPLOYMENT_ID"
)

// resourceAttributeKeys is every key Resource may set, for the label-keys
// gate: a resource attribute names the process, it is not a series label.
//
//nolint:gochecknoglobals // immutable set read by a test in this package.
var resourceAttributeKeys = map[string]bool{
	attrServiceName: true, attrServiceVersion: true, attrServiceInstanceID: true,
	attrDeploymentEnvironment: true, attrVCSRevision: true, attrDeploymentID: true,
}

// The one resource every signal carries, built on first use. The tracer, the
// meter provider and the log provider each take it, and the logger is built
// before the platform, so it is memoized here rather than threaded through.
//
//nolint:gochecknoglobals // process-wide memo; see Resource.
var (
	resourceMu    sync.Mutex
	resourceValue *resource.Resource
)

// Resource is the OpenTelemetry resource that identifies this process on
// every signal it emits (#1893): service.name (OTEL_SERVICE_NAME, default
// DefaultServiceName), service.version and vcs.ref.head.revision from the
// build, service.instance.id (the hostname, else a UUID minted at boot),
// deployment.environment.name and mcp_platform.deployment.id from the
// platform's own variables, merged over OTEL_RESOURCE_ATTRIBUTES so an
// operator's extra attributes are honored and the specific variables win.
// Built once and shared by the tracer, the meter provider and the log provider.
func Resource() *resource.Resource {
	resourceMu.Lock()
	defer resourceMu.Unlock()
	if resourceValue == nil {
		resourceValue = buildResource(context.Background(), os.Hostname)
	}
	return resourceValue
}

// buildResource assembles the resource from the environment and the build.
// Detectors merge in order, later winning: the default service name, then
// the standard variables, then the platform's own. A schema URL conflict on
// merge is reported and the merged resource used; it never stops startup.
func buildResource(ctx context.Context, hostname func() (string, error)) *resource.Resource {
	res, err := resource.New(ctx,
		resource.WithAttributes(attribute.String(attrServiceName, DefaultServiceName)),
		resource.WithFromEnv(),
		resource.WithAttributes(platformResourceAttributes(hostname)...),
	)
	if err != nil {
		slog.Warn("observability: resource attributes merged with a conflict; the merged resource is used", "error", err)
	}
	if res == nil {
		res = resource.Empty()
	}
	return res
}

// platformResourceAttributes is what the platform knows about itself: the
// build, the instance, and the two deployment variables when they are set.
func platformResourceAttributes(hostname func() (string, error)) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		attribute.String(attrServiceVersion, buildinfo.Version),
		attribute.String(attrVCSRevision, buildinfo.Commit),
		attribute.String(attrServiceInstanceID, instanceID(hostname)),
	}
	if v := stringEnvOrDefault(envDeploymentEnvironment, ""); v != "" {
		attrs = append(attrs, attribute.String(attrDeploymentEnvironment, v))
	}
	if v := stringEnvOrDefault(envDeploymentID, ""); v != "" {
		attrs = append(attrs, attribute.String(attrDeploymentID, v))
	}
	return attrs
}

// instanceID is the hostname (the pod name on Kubernetes), else a UUID: a
// process must be told apart from its peers even where the host has no name.
func instanceID(hostname func() (string, error)) string {
	if h, err := hostname(); err == nil && h != "" {
		return h
	}
	return uuid.NewString()
}

// DeploymentIDSet reports whether the resource names the deployment
// (mcp_platform.deployment.id), from either variable that can set it. A fleet
// backend receiving OTLP from several deployments cannot tell them apart
// without it, which is why startup warns when an OTLP exporter is on and this
// is false.
func DeploymentIDSet() bool {
	for _, kv := range Resource().Attributes() {
		if string(kv.Key) == attrDeploymentID && kv.Value.AsString() != "" {
			return true
		}
	}
	return false
}
