package script

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/libraryuse"
)

// TestALibraryIsNeverRunOrScheduled: a library has no main(); the scripts
// that load it are what runs (#1941).
func TestALibraryIsNeverRunOrScheduled(t *testing.T) {
	lib := &Script{ID: "s1", Name: "dates", Library: true, Enabled: true, Status: StatusActive}
	require.ErrorIs(t, RefuseRun(lib), errLibraryNotRun)
	require.ErrorIs(t, RefuseDraftRun(lib), errLibraryNotRun)
	_, err := BuildSchedule(lib, nil, ScheduleRequest{CronSpec: "0 6 * * *"}, time.Now())
	require.ErrorIs(t, err, errLibraryNotRun)
	assert.Contains(t, errLibraryNotRun.Error(), "manage_script command=test")

	lib.Library = false
	assert.NoError(t, RefuseRun(lib))
	assert.NoError(t, RefuseDraftRun(lib))
}

// TestALibrarysContractSaysItIsLoaded: the document a reference to a library
// resolves to says how it is loaded and who loads it, and a script's says
// what it loads.
func TestALibrarysContractSaysItIsLoaded(t *testing.T) {
	lib := BuildContract(&Script{Name: "dates", Version: 2, Library: true, Enabled: true, Status: StatusActive}, nil, nil)
	assert.True(t, lib.Library)
	assert.Equal(t, []libraryuse.Use{}, lib.UsedBy, "an empty list is [], never null")
	text := lib.Text()
	assert.Contains(t, text, `Library: other scripts load it by version, as load("lib:dates@2", ...)`)
	assert.Contains(t, text, "Used by: no script loads it.")

	lib.UsedBy = []libraryuse.Use{{Name: "weekly", Version: 1}}
	assert.Contains(t, lib.Text(), "Used by: weekly (version 1)")

	sc := BuildContract(&Script{Name: "weekly", Loads: []string{"dates@1", "fmt@3"}, Enabled: true, Status: StatusActive}, nil, nil)
	assert.Contains(t, sc.Text(), "Loads: lib:dates@1, lib:fmt@3")
	assert.NotContains(t, sc.Text(), "Used by")
}
