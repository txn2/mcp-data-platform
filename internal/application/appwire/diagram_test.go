package appwire

import (
	"context"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/application/appdiagram"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

func TestFilledTables(t *testing.T) {
	assert.Equal(t, []string{"products", "inventory", "stock", "seed", "prices"}, filledTables(`CREATE TABLE products (sku text);
		INSERT INTO products VALUES ('a'); insert into shop.inventory (sku) values ('a');
		UPDATE "stock" SET n = 1; COPY seed FROM STDIN; MERGE INTO prices USING x ON true`))
	assert.Empty(t, filledTables("CREATE TABLE t (id int)"))
}

type versions struct{ v *portaldomain.AssetVersion }

func (versions) ListByAsset(context.Context, string, int, int) ([]portaldomain.AssetVersion, int, error) {
	return nil, 0, nil
}

func (v versions) GetByVersion(context.Context, string, int) (*portaldomain.AssetVersion, error) {
	if v.v == nil {
		return nil, errors.New("none")
	}
	return v.v, nil
}

func (versions) GetLatestVersion(context.Context, string) (*portaldomain.AssetVersion, error) {
	return nil, errors.New("unused")
}

type blobs struct{ data string }

func (b blobs) GetObject(context.Context, string, string) (data []byte, contentType string, err error) {
	if b.data == "" {
		return nil, "", errors.New("none")
	}
	return []byte(b.data), "text/html", nil
}

func (blobs) PutObject(context.Context, string, string, []byte, string) error { return nil }

func TestAssetContentAndDeps(t *testing.T) {
	ctx := context.Background()
	read := assetContent(versions{&portaldomain.AssetVersion{S3Bucket: "b", S3Key: "k", SizeBytes: 10}}, blobs{"<a href=\"/endpoints/x\">"})
	assert.Contains(t, read(ctx, "a", 1), "/endpoints/x")
	assert.Empty(t, assetContent(versions{}, blobs{"x"})(ctx, "a", 1))
	assert.Empty(t, assetContent(versions{&portaldomain.AssetVersion{SizeBytes: maxDiagramContent + 1}}, blobs{"x"})(ctx, "a", 1))
	assert.Empty(t, assetContent(versions{&portaldomain.AssetVersion{}}, blobs{})(ctx, "a", 1))

	assert.Nil(t, diagramDeps(Deps{}).Bindings, "nothing without a database")
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	deps := diagramDeps(Deps{DB: db})
	assert.Nil(t, deps.AssetContent, "no asset store, no content")
	mock.ExpectQuery("FROM appdb_migrations").WillReturnRows(sqlmock.NewRows([]string{
		"connection", "schema_name", "version", "name", "up_sql", "down_sql", "transactional", "checksum", "author", "created_at", "updated_at",
	}).AddRow("orders", "shop", 1, "m", "INSERT INTO products VALUES (1)", "", true, "c", "a", time.Now(), time.Now()))
	assert.Equal(t, []string{"products"}, deps.SchemaWrites(ctx, "orders", "shop"))
	mock.ExpectQuery("FROM appdb_migrations").WillReturnError(errors.New("down"))
	assert.Nil(t, deps.SchemaWrites(ctx, "orders", "shop"))

	// Every endpoint a script holds is on its card (#2441).
	cols := []string{
		"id", "script_id", "name", "methods", "path", "auth_mode", "callers", "bot_check", "checked_methods", "timeout_seconds",
		"max_memory_bytes", "max_body_bytes", "allowed_origins", "rate_limit", "caller_rate_limit", "enabled",
		"created_by", "updated_by", "created_at", "updated_at", "response_mode", "page_cdns",
		"page_frame_origins", "page_connect_origins", "page_form_origins", "ip_rate_limit",
	}
	row := func(id, name, path string) []driver.Value {
		return []driver.Value{
			id, "s1", name, "{GET}", path, "authenticated", "{}", "", "{}", 10, int64(1), 1, "{}", 60, 6, true,
			"a@example.com", "a@example.com", time.Now(), time.Now(), "api", "{}", "{}", "{}", "{}", 0,
		}
	}
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1 ORDER BY name")).WithArgs("s1").
		WillReturnRows(sqlmock.NewRows(cols).AddRow(row("e1", "orders", "/")...).AddRow(row("e2", "orders-v2", "/{id}")...))
	held, err := deps.Bindings(ctx, "s1")
	require.NoError(t, err)
	assert.Equal(t, []appdiagram.Binding{{Name: "orders", Path: "/"}, {Name: "orders-v2", Path: "/{id}"}}, held)
	mock.ExpectQuery(regexp.QuoteMeta("WHERE script_id = $1 ORDER BY name")).WillReturnError(errors.New("down"))
	_, err = deps.Bindings(ctx, "s1")
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
