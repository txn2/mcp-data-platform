package observability

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
)

// resetResourceForTest drops the memoized resource so a test's environment
// is what the next Resource() reads, and restores the drop when the test
// ends so later tests start clean.
func resetResourceForTest(t *testing.T) {
	t.Helper()
	drop := func() {
		resourceMu.Lock()
		defer resourceMu.Unlock()
		resourceValue = nil
	}
	drop()
	t.Cleanup(drop)
}

func resourceMap(t *testing.T, attrs []attribute.KeyValue) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, kv := range attrs {
		out[string(kv.Key)] = kv.Value.AsString()
	}
	return out
}

func TestResource_CarriesTheBuildTheInstanceAndTheDeployment(t *testing.T) {
	resetResourceForTest(t)
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "deployment.environment.name=staging,mcp_platform.deployment.id=a,team=data")
	t.Setenv(envDeploymentEnvironment, "")
	t.Setenv(envDeploymentID, "")

	got := resourceMap(t, Resource().Attributes())
	assert.Equal(t, DefaultServiceName, got["service.name"], "the default applies when no variable names the service")
	assert.Equal(t, "dev", got["service.version"], "buildinfo.Version of an unstamped build")
	assert.Equal(t, "none", got["vcs.ref.head.revision"], "buildinfo.Commit of an unstamped build")
	assert.NotEmpty(t, got["service.instance.id"])
	assert.Equal(t, "staging", got["deployment.environment.name"], "OTEL_RESOURCE_ATTRIBUTES is honored")
	assert.Equal(t, "a", got["mcp_platform.deployment.id"])
	assert.Equal(t, "data", got["team"], "an operator's extra attribute is kept")
	assert.True(t, DeploymentIDSet())
	assert.Same(t, Resource(), Resource(), "built once")
}

func TestResource_ThePlatformVariablesWinOverTheStandardOnes(t *testing.T) {
	resetResourceForTest(t)
	t.Setenv("OTEL_SERVICE_NAME", "named-by-env")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=named-in-list,deployment.environment.name=staging,mcp_platform.deployment.id=a")
	t.Setenv(envDeploymentEnvironment, "prod")
	t.Setenv(envDeploymentID, "fleet-7")

	got := resourceMap(t, Resource().Attributes())
	assert.Equal(t, "named-by-env", got["service.name"], "OTEL_SERVICE_NAME wins over service.name in OTEL_RESOURCE_ATTRIBUTES, per the specification")
	assert.Equal(t, "prod", got["deployment.environment.name"])
	assert.Equal(t, "fleet-7", got["mcp_platform.deployment.id"])
}

func TestResource_NoDeploymentIDIsReported(t *testing.T) {
	resetResourceForTest(t)
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	t.Setenv(envDeploymentEnvironment, "")
	t.Setenv(envDeploymentID, "")

	got := resourceMap(t, Resource().Attributes())
	_, hasEnv := got["deployment.environment.name"]
	_, hasID := got["mcp_platform.deployment.id"]
	assert.False(t, hasEnv, "an unset environment is absent, not empty")
	assert.False(t, hasID)
	assert.False(t, DeploymentIDSet())
}

func TestInstanceID_FallsBackToAUUIDWithoutAHostname(t *testing.T) {
	assert.Equal(t, "pod-7", instanceID(func() (string, error) { return "pod-7", nil }))
	id := instanceID(func() (string, error) { return "", errors.New("no hostname") })
	require.Len(t, id, 36, "a UUID when the host has no name: %q", id)
	assert.NotEqual(t, id, instanceID(func() (string, error) { return "", nil }), "minted per call, never reused")
}

func TestBuildResource_SurvivesADetectorError(t *testing.T) {
	// A hostname error is absorbed by instanceID; buildResource itself only
	// sees errors from the SDK's merge, which these detectors cannot raise.
	// The contract under test is that it always returns a usable resource.
	res := buildResource(context.Background(), func() (string, error) { return "", errors.New("down") })
	require.NotNil(t, res)
	assert.NotEmpty(t, resourceMap(t, res.Attributes())["service.instance.id"])
}
