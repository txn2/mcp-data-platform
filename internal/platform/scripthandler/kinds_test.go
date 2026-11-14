package scripthandler

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/endpoint/epbinding"
	"github.com/txn2/mcp-data-platform/internal/endpoint/epcmd"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents/sourcerule"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

func TestKindsAndTypes(t *testing.T) {
	assert.Equal(t, []string{KindEndpoint, KindSchedule, KindWebhook}, Kinds())
	assert.Equal(t, []string{KindEndpoint, TypeForm, KindSchedule, KindWebhook, TypeNone}, Types())

	for _, name := range Kinds() {
		k, ok := KindOf(name)
		require.True(t, ok, name)
		assert.Equal(t, name, k.Name)
		assert.NotEmpty(t, k.Contract, name)
		assert.NotEmpty(t, k.Settings, name)
		assert.NotEmpty(t, k.Creator, name)
	}
	_, ok := KindOf(TypeForm)
	assert.False(t, ok, "form is a type, not a kind: an endpoint whose script is a form")

	endpoint, _ := KindOf(KindEndpoint)
	require.NotNil(t, endpoint.Check, "an endpoint's contract is checked at every save")
	require.Error(t, endpoint.Check(mainSource))
	require.NoError(t, endpoint.Check(endpointSource))
	require.NoError(t, endpoint.Check(formSource), "a form answers through form_spec and form_submit")
}

func TestCheckKind(t *testing.T) {
	k, err := checkKind(KindSchedule)
	require.NoError(t, err)
	assert.Equal(t, KindSchedule, k.Name)

	_, err = checkKind("")
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), "kind is required: one of endpoint, schedule, webhook")

	_, err = checkKind("policy")
	require.ErrorIs(t, err, ErrUnknownKind)
	assert.Contains(t, err.Error(), `"policy": one of endpoint, schedule, webhook`)
}

func TestCheckTypes(t *testing.T) {
	require.NoError(t, CheckTypes(nil))
	require.NoError(t, CheckTypes(Types()))
	err := CheckTypes([]string{"schedule", "Form"})
	require.ErrorIs(t, err, ErrInvalid)
	assert.Contains(t, err.Error(), `unknown handler type "Form"`)
}

func TestIsForm(t *testing.T) {
	assert.True(t, isForm(formSource))
	assert.False(t, isForm(endpointSource))
	assert.False(t, isForm("def ("), "a source that does not parse is no form")
}

// TestErrorClassifiers pins how every surface reads a refusal: a caller not
// allowed (403), something missing (404) and the platform's own failure
// (500), by any kind's sentinel.
func TestErrorClassifiers(t *testing.T) {
	cases := []struct {
		name                     string
		err                      error
		refused, missing, failed bool
	}{
		{name: "not allowed", err: ErrNotAllowed, refused: true},
		{name: "endpoint not allowed", err: fmt.Errorf("x: %w", epbinding.ErrNotAllowed), refused: true},
		{name: "source refused", err: fmt.Errorf("x: %w", sourcerule.ErrSourceRefused), refused: true},
		{name: "not found", err: ErrNotFound, missing: true},
		{name: "binding not found", err: epbinding.ErrNotFound, missing: true},
		{name: "schedule not found", err: script.ErrScheduleNotFound, missing: true},
		{name: "webhook not found", err: scriptevents.ErrHandlerNotFound, missing: true},
		{name: "store", err: errors.Join(ErrStore, errDown), failed: true},
		{name: "endpoint store", err: fmt.Errorf("x: %w", epcmd.ErrStore), failed: true},
		{name: "invalid", err: ErrInvalid},
		{name: "anything else", err: errDown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.refused, Refused(tc.err), "refused")
			assert.Equal(t, tc.missing, Missing(tc.err), "missing")
			assert.Equal(t, tc.failed, Failed(tc.err), "failed")
		})
	}
}

func TestStoreOr(t *testing.T) {
	for _, passed := range []error{ErrNotFound, script.ErrScheduleNotFound, ErrNotAllowed, sourcerule.ErrSourceRefused, ErrInvalid} {
		got := storeOr(passed)
		require.ErrorIs(t, got, passed)
		assert.False(t, Failed(got), "%v is the caller's, not the store's", passed)
	}
	got := storeOr(errDown)
	require.ErrorIs(t, got, ErrStore)
	require.ErrorIs(t, got, errDown)
	assert.Equal(t, errDown.Error(), got.Error(), "the store's failure keeps the text the log records")
}
