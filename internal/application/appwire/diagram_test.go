package appwire

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	assert.Nil(t, diagramDeps(Deps{}).Binding, "nothing without a database")
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
}
