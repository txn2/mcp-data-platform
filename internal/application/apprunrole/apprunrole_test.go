package apprunrole

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application"
	"github.com/txn2/mcp-data-platform/internal/application/appsvc"
	"github.com/txn2/mcp-data-platform/pkg/platform/fieldcrypt"
	"github.com/txn2/mcp-data-platform/pkg/registry"
)

// holdingStore answers Holding and RunRoleOf; the rest of application.Store
// is never called here.
type holdingStore struct {
	application.Store
	apps  map[[2]string]string
	roles map[string]application.RunRole
	err   error
}

func (s holdingStore) Holding(_ context.Context, connection, schema string) (*application.Application, error) {
	if s.err != nil {
		return nil, s.err
	}
	if name, ok := s.apps[[2]string{connection, schema}]; ok {
		return &application.Application{Name: name}, nil
	}
	return nil, application.ErrNotFound
}

func (s holdingStore) RunRoleOf(_ context.Context, id string) (*application.RunRole, error) {
	if s.err != nil {
		return nil, s.err
	}
	if r, ok := s.roles[id]; ok {
		return &r, nil
	}
	return nil, application.ErrNoRunRole
}

// TestRunRoles is what the toolkit asks (#2539): which application holds a
// schema, and an application's run role with its password opened only for
// the binding it was sealed under.
func TestRunRoles(t *testing.T) {
	key := make([]byte, fieldcrypt.KeyLength)
	enc, err := fieldcrypt.NewFieldEncryptor(key)
	require.NoError(t, err)
	sealer := fieldcrypt.NewRestFieldEncryptor(enc)
	sealed, err := sealer.SealBound(appsvc.RunRoleBinding("app_1", "orders", "shop", "shop_run"), "s3cret")
	require.NoError(t, err)
	store := holdingStore{
		apps: map[[2]string]string{{"orders", "shop"}: "shop"},
		roles: map[string]application.RunRole{
			"app_1": {ApplicationID: "app_1", Connection: "orders", Schema: "shop", Role: "shop_run", SealedPassword: sealed},
			// A value sealed for app_1, copied onto app_2's row, does not open.
			"app_2": {ApplicationID: "app_2", Connection: "orders", Schema: "shop", Role: "shop_run", SealedPassword: sealed},
		},
	}
	h := New(store, sealer)
	ctx := context.Background()

	name, err := h.Holder(ctx, "orders", "shop")
	require.NoError(t, err)
	assert.Equal(t, "shop", name)
	name, err = h.Holder(ctx, "orders", "none")
	require.NoError(t, err)
	assert.Empty(t, name)

	role, password, err := h.RunRole(ctx, "orders", "app_1")
	require.NoError(t, err)
	assert.Equal(t, "shop_run", role)
	assert.Equal(t, "s3cret", password)
	role, _, err = h.RunRole(ctx, "ledger", "app_1")
	require.NoError(t, err)
	assert.Empty(t, role, "a run role is only for the connection holding the schema")
	role, _, err = h.RunRole(ctx, "orders", "app_9")
	require.NoError(t, err)
	assert.Empty(t, role)
	_, _, err = h.RunRole(ctx, "orders", "app_2")
	require.ErrorContains(t, err, "opening the run role's password", "a sealed value opens only for its own binding")
	assert.NotContains(t, err.Error(), "s3cret")

	failing := New(holdingStore{err: errors.New("down")}, sealer)
	_, err = failing.Holder(ctx, "orders", "shop")
	require.Error(t, err)
	_, _, err = failing.RunRole(ctx, "orders", "app_1")
	require.Error(t, err)

	assert.Nil(t, New(nil, sealer).Holder)
	assert.Nil(t, Over(nil, sealer).RunRole)
}

// TestSessionApplications answers a session's run-as application and a
// schema's holder from the store (#2539).
func TestSessionApplications(t *testing.T) {
	assert.Nil(t, SessionApplications(nil).RunAs)
	assert.Nil(t, ReachVia(nil, nil))
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	apps := SessionApplications(db)
	cols := []string{
		"id", "name", "description", "owner_email", "connection", "schema_name", "current_release",
		"created_by", "created_at", "updated_at", "schema_adopted_version", "schema_added_by", "schema_added_at",
	}
	app := func() *sqlmock.Rows {
		return sqlmock.NewRows(cols).AddRow("app_1", "shop", "", "j@x", "orders", "shop", 0, "j@x", time.Now(), time.Now(), 0, "j@x", time.Now())
	}
	noParts := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"kind", "part_id", "added_by", "added_at"}) }
	ctx := context.Background()

	mock.ExpectQuery("SELECT application_id FROM application_parts").WillReturnRows(sqlmock.NewRows([]string{"application_id"}).AddRow("app_1"))
	mock.ExpectQuery("FROM applications WHERE id").WillReturnRows(app())
	mock.ExpectQuery("FROM application_parts").WillReturnRows(noParts())
	assert.Equal(t, "app_1", apps.RunAs(ctx, "s1", "orders"))
	mock.ExpectQuery("SELECT application_id FROM application_parts").WillReturnRows(sqlmock.NewRows([]string{"application_id"}).AddRow("app_1"))
	mock.ExpectQuery("FROM applications WHERE id").WillReturnRows(app())
	mock.ExpectQuery("FROM application_parts").WillReturnRows(noParts())
	assert.Empty(t, apps.RunAs(ctx, "s1", "ledger"), "a run role is only for the connection holding the schema")
	mock.ExpectQuery("SELECT application_id FROM application_parts").WillReturnRows(sqlmock.NewRows([]string{"application_id"}))
	assert.Empty(t, apps.RunAs(ctx, "s2", "orders"))

	mock.ExpectQuery("WHERE connection = ").WillReturnRows(app())
	mock.ExpectQuery("FROM application_parts").WillReturnRows(noParts())
	id, name := apps.Holder(ctx, "orders", "shop")
	assert.Equal(t, [2]string{"app_1", "shop"}, [2]string{id, name})
	mock.ExpectQuery("WHERE connection = ").WillReturnRows(sqlmock.NewRows(cols))
	id, _ = apps.Holder(ctx, "orders", "none")
	assert.Empty(t, id)
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestReach lists the appdb connections an actor reaches: none from an
// empty registry.
func TestReach(t *testing.T) {
	reach := Reach(registry.NewRegistry(), nil, nil)
	require.NotNil(t, reach)
	got, err := reach(context.Background(), application.Actor{Email: "j@x", Roles: []string{"analyst"}})
	require.NoError(t, err)
	assert.Empty(t, got)
	got, err = reach(context.Background(), application.Actor{Email: "a@x", Admin: true})
	require.NoError(t, err)
	assert.Empty(t, got)
}
