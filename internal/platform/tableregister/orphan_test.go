package tableregister

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The table testSource registers as, as the coordinator names it.
const orphanTable = `"scratch"."uploads"."analyst_content"`

// abandonedCreate makes the coordinator complete a CREATE the platform stopped
// waiting for: the table is made, and the client reports its deadline. It is
// the failure #2131 was observed with.
func abandonedCreate(h *harness, cause error) {
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, createTablePrefix) {
			h.trino.hold(orphanTable, externalLocationIn(sql))
			return fmt.Errorf("query failed: %w", cause)
		}
		return nil
	}
}

func register(t *testing.T, h *harness, src Source) (*Result, error) {
	t.Helper()
	return h.reg.Register(context.Background(), testCaller(), src, Request{Connection: "scratch", Source: "mcp"})
}

// TestRegister_AdoptsATableAnEarlierRegistrationOfTheFileLeft is #2131's
// acceptance: a table that an abandoned registration left with no record, over
// this file's directory, no longer refuses the next registration "already
// exists". It is replaced, recorded, and the replacement is audited.
func TestRegister_AdoptsATableAnEarlierRegistrationOfTheFileLeft(t *testing.T) {
	h := newHarness(t)
	h.trino.hold(orphanTable, "s3://portal-assets/artifacts/u1/asset_1/")

	res, err := register(t, h, testSource())
	require.NoError(t, err)

	require.Len(t, h.trino.statements, 4)
	assert.True(t, strings.HasPrefix(h.trino.statements[1], createTablePrefix), "the CREATE that was refused")
	assert.Equal(t, "DROP TABLE IF EXISTS "+orphanTable, h.trino.statements[2])
	assert.True(t, strings.HasPrefix(h.trino.statements[3], createTablePrefix))

	stored, err := h.store.ByName(context.Background(), "scratch", "scratch", "uploads", "analyst_content")
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, res.ID, stored.ID)

	require.Len(t, h.audit.events, 1)
	assert.True(t, h.audit.events[0].Success)
	assert.Contains(t, h.audit.events[0].Parameters["sql"], "DROP TABLE IF EXISTS "+orphanTable)
}

// TestRegister_AdoptsATableOverAnEarlierVersionOfTheFile: a scheduled export
// writes each run's file as a new version under its own directory, so the
// table left by an earlier run reads a sibling of the current directory, still
// inside the one the source's id names.
func TestRegister_AdoptsATableOverAnEarlierVersionOfTheFile(t *testing.T) {
	src := Source{
		Kind: KindResource, ID: "res_9", Name: "daily.csv", Bucket: "resources",
		HeadKey:     "resources/user/u1/res_9/v/rev2/daily.csv",
		ContentType: "text/csv",
	}
	h := newHarness(t, func(h *harness) {
		h.objects.entries = []ObjectEntry{{Key: src.HeadKey}}
	})
	h.trino.hold(`"scratch"."uploads"."analyst_daily"`, "s3://resources/resources/user/u1/res_9/v/rev1/")

	res, err := register(t, h, src)
	require.NoError(t, err)
	assert.Equal(t, "s3://resources/resources/user/u1/res_9/v/rev2/", res.Location)
	assert.Equal(t, res.Location, h.trino.locations[`"scratch"."uploads"."analyst_daily"`])
}

// TestRegister_ATableThatIsNotTheFilesIsStillRefused: a name held by a table
// over anything else is a real collision, refused as one, and nothing is
// dropped.
func TestRegister_ATableThatIsNotTheFilesIsStillRefused(t *testing.T) {
	for name, location := range map[string]string{
		"another file":         "s3://portal-assets/artifacts/u2/asset_2/",
		"a prefix of the id":   "s3://portal-assets/artifacts/u1/asset_10/",
		"another bucket":       "s3://elsewhere/artifacts/u1/asset_1/",
		"no external location": "",
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.trino.hold(orphanTable, location)

			_, err := register(t, h, testSource())
			require.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), "already exists in the scratch schema and no registration holds it")
			assert.Contains(t, err.Error(), "choose another table name")
			for _, stmt := range h.trino.statements {
				assert.False(t, strings.HasPrefix(stmt, dropTablePrefix), "a table that is not the file's is never dropped")
			}
			assert.True(t, h.trino.tables[orphanTable])
			assert.Empty(t, h.store.rows)
		})
	}
}

// TestRegister_AnOrphanThatCannotBeReadIsAFailure: a lookup that fails leaves
// the table alone and reports both what was refused and why it could not be
// settled.
func TestRegister_AnOrphanThatCannotBeReadIsAFailure(t *testing.T) {
	h := newHarness(t)
	h.trino.hold(orphanTable, "s3://portal-assets/artifacts/u1/asset_1/")
	h.trino.locationErr = errors.New("coordinator unavailable")

	_, err := register(t, h, testSource())
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRefused)
	assert.Contains(t, err.Error(), "already exists")
	assert.Contains(t, err.Error(), "coordinator unavailable")
	assert.Len(t, h.trino.statements, 2, "nothing is dropped")
}

// TestRegister_AnOrphanGoneBeforeItIsReadIsReportedAsItWas: the table was
// taken away between the refusal and the look, so there is nothing to adopt
// and the refusal stands; the next registration creates the table.
func TestRegister_AnOrphanGoneBeforeItIsReadIsReportedAsItWas(t *testing.T) {
	h := newHarness(t)
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, createTablePrefix) {
			return errors.New("line 1:1: Table '" + orphanTable + "' already exists")
		}
		return nil
	}

	_, err := register(t, h, testSource())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
	assert.Len(t, h.trino.statements, 2)
}

// TestRegister_AnAbandonedCreateTakesItsTableBackOut: a CREATE the platform
// stopped waiting for is followed by a drop of the table it may have made, so
// the next registration starts from nothing, and the drop is audited.
func TestRegister_AnAbandonedCreateTakesItsTableBackOut(t *testing.T) {
	for name, cause := range map[string]error{
		"deadline": context.DeadlineExceeded,
		"canceled": context.Canceled,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			abandonedCreate(h, cause)

			_, err := register(t, h, testSource())
			require.ErrorIs(t, err, cause)
			assert.Equal(t, "DROP TABLE IF EXISTS "+orphanTable, h.trino.statements[len(h.trino.statements)-1])
			assert.False(t, h.trino.tables[orphanTable])
			require.Len(t, h.audit.events, 2)
			assert.Equal(t, "DROP TABLE IF EXISTS "+orphanTable, h.audit.events[1].Parameters["sql"])

			h.trino.errFor = nil
			_, err = register(t, h, testSource())
			require.NoError(t, err, "the next registration of the file succeeds")
		})
	}
}

// TestRegister_ACreateAbandonedByTheCallerIsCleanedUpAfterThem: the caller
// going away mid-CREATE is an abandonment too, and the drop runs on a context
// their cancellation does not reach.
func TestRegister_ACreateAbandonedByTheCallerIsCleanedUpAfterThem(t *testing.T) {
	h := newHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, createTablePrefix) {
			h.trino.hold(orphanTable, externalLocationIn(sql))
			cancel()
			return errors.New("query failed: the request was interrupted")
		}
		return nil
	}

	_, err := h.reg.Register(ctx, testCaller(), testSource(), Request{Connection: "scratch"})
	require.Error(t, err)
	assert.False(t, h.trino.tables[orphanTable])
}

// TestRegister_AnAbandonedCreateLeavesATableThatIsNotItsOwn: what is under
// the name after an abandoned CREATE is dropped only when it reads this
// file's directory; otherwise, and when it cannot be read, it is left.
func TestRegister_AnAbandonedCreateLeavesATableThatIsNotItsOwn(t *testing.T) {
	t.Run("another file", func(t *testing.T) {
		h := newHarness(t)
		h.trino.errFor = func(sql string) error {
			if strings.HasPrefix(sql, createTablePrefix) {
				h.trino.hold(orphanTable, "s3://portal-assets/artifacts/u2/asset_2/")
				return fmt.Errorf("query failed: %w", context.DeadlineExceeded)
			}
			return nil
		}
		_, err := register(t, h, testSource())
		require.Error(t, err)
		assert.True(t, h.trino.tables[orphanTable])
		assert.Len(t, h.audit.events, 1)
	})
	t.Run("nothing there", func(t *testing.T) {
		h := newHarness(t)
		h.trino.errFor = func(sql string) error {
			if strings.HasPrefix(sql, createTablePrefix) {
				return fmt.Errorf("query failed: %w", context.DeadlineExceeded)
			}
			return nil
		}
		_, err := register(t, h, testSource())
		require.Error(t, err)
		assert.Len(t, h.trino.statements, 2, "no drop of a table that is not there")
	})
	t.Run("cannot be read", func(t *testing.T) {
		h := newHarness(t)
		abandonedCreate(h, context.DeadlineExceeded)
		h.trino.locationErr = errors.New("coordinator unavailable")
		_, err := register(t, h, testSource())
		require.Error(t, err)
		assert.True(t, h.trino.tables[orphanTable], "a table that cannot be proven the file's is left")
	})
}

// TestRegister_AnAbandonedCreateWhoseDropFailsStillAnswers: the drop is best
// effort; its failure is audited and the registration's own error stands.
func TestRegister_AnAbandonedCreateWhoseDropFailsStillAnswers(t *testing.T) {
	h := newHarness(t)
	abandonedCreate(h, context.DeadlineExceeded)
	create := h.trino.errFor
	h.trino.errFor = func(sql string) error {
		if strings.HasPrefix(sql, dropTablePrefix) {
			return errors.New("metastore unavailable")
		}
		return create(sql)
	}

	_, err := register(t, h, testSource())
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Len(t, h.audit.events, 2)
	assert.False(t, h.audit.events[1].Success)
	assert.True(t, h.trino.tables[orphanTable])
}

// TestSettleCreate_LeavesOtherFailuresAlone: a failure before the CREATE, and
// one of the CREATE that is neither an abandonment nor a name already held,
// are returned unchanged with nothing looked up.
func TestSettleCreate_LeavesOtherFailuresAlone(t *testing.T) {
	t.Run("schema", func(t *testing.T) {
		h := newHarness(t)
		h.trino.errFor = func(sql string) error {
			if strings.HasPrefix(sql, "CREATE SCHEMA") {
				return fmt.Errorf("query failed: %w", context.DeadlineExceeded)
			}
			return nil
		}
		h.trino.locationErr = errors.New("must not be asked")
		_, err := register(t, h, testSource())
		require.ErrorIs(t, err, context.DeadlineExceeded)
		assert.Len(t, h.trino.statements, 1)
	})
	t.Run("access denied", func(t *testing.T) {
		h := newHarness(t)
		h.trino.errFor = func(sql string) error {
			if strings.HasPrefix(sql, createTablePrefix) {
				return errors.New("Access Denied: Cannot create table")
			}
			return nil
		}
		h.trino.locationErr = errors.New("must not be asked")
		_, err := register(t, h, testSource())
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "must not be asked")
		assert.Len(t, h.trino.statements, 2)
	})
}

// TestAuditStatement: a statement run outside a registration is audited the
// way the registrar audits its own, under the source given.
func TestAuditStatement(t *testing.T) {
	h := newHarness(t)
	h.reg.AuditStatement(context.Background(), Statement{
		Caller: Caller{Email: "admin@example.com", IsAdmin: true}, Source: "admin",
		Registration: Registration{Connection: "scratch", Catalog: "scratch", Schema: "uploads", Table: "left_over"},
		DDL:          []string{"DROP TABLE IF EXISTS " + orphanTable},
	})
	require.Len(t, h.audit.events, 1)
	ev := h.audit.events[0]
	assert.Equal(t, "admin", ev.Source)
	assert.Equal(t, "admin@example.com", ev.UserEmail)
	assert.Equal(t, "scratch.uploads.left_over", ev.Parameters["table"])
	assert.True(t, ev.Success)
}

// TestRegister_ANameRecordedMeanwhileKeepsItsTable: when another registration
// of the same file records the name while this one runs, this one fails
// without dropping the table that registration now names.
func TestRegister_ANameRecordedMeanwhileKeepsItsTable(t *testing.T) {
	h := newHarness(t)
	h.store.onInsert = func() {
		h.store.onInsert = nil
		h.store.rows["reg_other"] = Registration{
			ID: "reg_other", SourceKind: KindAsset, SourceID: "asset_1", Connection: "scratch",
			Catalog: "scratch", Schema: "uploads", Table: "analyst_content", RegisteredBy: "alice@example.com",
		}
	}
	_, err := register(t, h, testSource())
	require.ErrorIs(t, err, ErrNameTaken)
	assert.True(t, h.trino.tables[orphanTable], "the table the other registration names is left in place")
	for _, stmt := range h.trino.statements {
		assert.False(t, strings.HasPrefix(stmt, dropTablePrefix), "nothing is dropped: %s", stmt)
	}
}
