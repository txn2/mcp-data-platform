package scratchcatalog

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The engine's answers, as Trino words them.
const (
	deniedAnswer   = "USER_ERROR: Access Denied: Cannot execute procedure scratch.system.unregister_partition"
	disabledAnswer = "register_partition procedure is disabled"
)

func TestDeniedReadsTheProcedureTrinoNames(t *testing.T) {
	got, ok := Denied(errors.New(deniedAnswer))
	require.True(t, ok)
	assert.Equal(t, "scratch.system.unregister_partition", got)

	for _, err := range []error{nil, errors.New("Access Denied: Cannot select from table x"), errors.New(disabledAnswer)} {
		_, ok := Denied(err)
		assert.False(t, ok, "%v is not a procedure refusal", err)
	}
}

func TestDisabled(t *testing.T) {
	assert.True(t, Disabled(errors.New(disabledAnswer)))
	assert.False(t, Disabled(errors.New(deniedAnswer)))
	assert.False(t, Disabled(nil))
}

func TestClassifyNamesTheSettingThatFixesEachRefusal(t *testing.T) {
	denied := Classify("scratch", "scratch", fmt.Errorf("unregistering window: %w", errors.New(deniedAnswer)))
	require.ErrorIs(t, denied, ErrProcedureDenied)
	assert.Equal(t,
		"the Trino user of connection scratch may not EXECUTE scratch.system.unregister_partition; "+
			"a catalog rule does not grant procedures, so add a procedures rule for that user on the catalog's system schema "+
			"(see docs/server/scratch-catalog.md#access-control): unregistering window: "+deniedAnswer,
		denied.Error())

	disabled := Classify("scratch", "lake", errors.New(disabledAnswer))
	require.ErrorIs(t, disabled, ErrRegisterDisabled)
	assert.Contains(t, disabled.Error(), "the lake catalog does not allow register_partition; set hive.allow-register-partition-procedure=true")
	assert.Contains(t, disabled.Error(), "every worker")
	assert.Contains(t, disabled.Error(), DocsCatalog)

	other := errors.New("boom")
	assert.Same(t, other, Classify("scratch", "scratch", other), "any other error is returned as it is")
}
