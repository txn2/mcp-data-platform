package tableregister

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/parquet-go/parquet-go"

	"github.com/txn2/mcp-data-platform/internal/tableparquet"
	"github.com/txn2/mcp-data-platform/internal/tabletype"
)

// parquetFile writes a small Parquet file with the given columns and one row.
func parquetFile(t *testing.T, cols ...tabletype.Column) []byte {
	t.Helper()
	row := make([]any, len(cols))
	b, err := tableparquet.Write(cols, [][]any{row})
	require.NoError(t, err)
	return b
}

// caseCollidingParquet writes a file whose columns are one apart by case. It
// goes to the Parquet writer directly because tableparquet.Write refuses such
// a file, and this is about what the READER does with one that exists: another
// writer's file is not held to what this platform will write.
func caseCollidingParquet(t *testing.T) []byte {
	t.Helper()
	root := parquet.Group{"Id": parquet.Optional(parquet.Int(64)), "id": parquet.Optional(parquet.String())}
	var buf bytes.Buffer
	w := parquet.NewWriter(&buf, parquet.NewSchema("t", root))
	require.NoError(t, w.Close())
	return buf.Bytes()
}

func col(t *testing.T, name, typ string) tabletype.Column {
	t.Helper()
	parsed, err := tabletype.Parse(typ)
	require.NoError(t, err)
	return tabletype.Column{Name: name, Type: parsed}
}

func parquetSource(key string) Source {
	src := testSource()
	src.HeadKey = key
	src.ContentType = tableparquet.ContentType
	return src
}

// parquetHarness serves body as the Parquet file parquetSource names.
func parquetHarness(t *testing.T, body []byte) *harness {
	t.Helper()
	key := parquetSource("artifacts/u1/asset_1/rows.parquet").HeadKey
	return newHarness(t, func(h *harness) {
		h.objects = &fakeObjects{body: body, bodyCT: tableparquet.ContentType, entries: []ObjectEntry{{Key: key}}}
	})
}

// TestRegister_ParquetDeclaresTheFootersColumns: a Parquet file registers
// over the Parquet reader with the columns and types its footer declares, and
// its body is never read whole -- only ranged reads reach the store.
func TestRegister_ParquetDeclaresTheFootersColumns(t *testing.T) {
	body := parquetFile(t, col(t, "Id", "bigint"), col(t, "amount", "decimal(12,2)"), col(t, "at", "timestamp(6)"),
		col(t, "tags", "array(varchar)"))
	h := parquetHarness(t, body)
	h.objects.getErr = nil

	reg, err := h.reg.Register(context.Background(), testCaller(), parquetSource("artifacts/u1/asset_1/rows.parquet"),
		Request{Connection: "scratch", Source: "mcp", Follow: true, Repair: true})
	require.NoError(t, err)

	assert.Equal(t, FormatParquet, reg.Format)
	assert.Equal(t, []string{
		`CREATE SCHEMA IF NOT EXISTS "scratch"."uploads"`,
		`CREATE TABLE "scratch"."uploads"."analyst_rows" ("id" BIGINT, "amount" DECIMAL(12,2), "at" TIMESTAMP(6), ` +
			`"tags" ARRAY(VARCHAR)) WITH (external_location = 's3://portal-assets/artifacts/u1/asset_1/', format = 'PARQUET')`,
	}, h.trino.statements)
	assert.Positive(t, h.objects.ranged, "the footer is read by range")
	assert.Nil(t, reg.Correction, "repair has nothing to correct in a Parquet file and is accepted as a no-op")
	assert.Empty(t, h.reviser.saved, "a Parquet file is never rewritten")
	assert.True(t, reg.Repair, "the choice is still recorded")
}

// TestRegister_ParquetLargerThanTheReadCapRegisters: the cap a CSV is read
// whole under does not apply to a Parquet file, whose footer is all that is
// read.
func TestRegister_ParquetLargerThanTheReadCapRegisters(t *testing.T) {
	h := parquetHarness(t, parquetFile(t, col(t, "id", "bigint")))
	h.reg.deps.MaxBytes = 16

	_, err := h.reg.Register(context.Background(), testCaller(), parquetSource("artifacts/u1/asset_1/rows.parquet"),
		Request{Connection: "scratch", Source: "mcp"})
	require.NoError(t, err)
}

func TestRegister_ParquetRefusals(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want string
	}{
		{"bytes that are not Parquet", []byte("id,name\n1,a\n"), "the file is named or typed as Parquet, but the file is not a Parquet file"},
		{"columns one apart by case", caseCollidingParquet(t), `the columns "Id" and "id"`},
		// An empty object: a store answers the ranged read of one with
		// InvalidRange, which is not the platform's failure to report.
		{"an empty file", []byte{}, "it is 0 bytes, shorter than the smallest Parquet file"},
		// The four bytes before the trailing magic are the footer's length,
		// and the reader allocates them before reading them.
		{"a footer longer than the file", []byte("PAR1\x00\x00\x00\x00\xff\xff\xff\xffPAR1"), "does not fit in a file of 16 bytes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := parquetHarness(t, tt.body)
			_, err := h.reg.Register(context.Background(), testCaller(), parquetSource("artifacts/u1/asset_1/rows.parquet"),
				Request{Connection: "scratch", Source: "mcp"})
			require.Error(t, err)
			assert.ErrorIs(t, err, ErrRefused)
			assert.Contains(t, err.Error(), tt.want)
			assert.Empty(t, h.trino.statements, "a refused registration runs no statement")
		})
	}
}

// TestRegister_ParquetReadFailureIsThePlatforms: a store that fails the ranged
// read is a platform failure at the reading stage, not a refusal of the file.
func TestRegister_ParquetReadFailureIsThePlatforms(t *testing.T) {
	h := parquetHarness(t, parquetFile(t, col(t, "id", "bigint")))
	h.objects.getErr = errors.New("connection reset")

	_, err := h.reg.Register(context.Background(), testCaller(), parquetSource("artifacts/u1/asset_1/rows.parquet"),
		Request{Connection: "scratch", Source: "mcp"})
	require.Error(t, err)
	assert.NotErrorIs(t, err, ErrRefused)
	assert.Equal(t, "reading the file", StageOf(err))
}

// TestFollowSource_AParquetVersionWithANewColumnSaysSo: the follow re-reads
// the footer, moves the table, and the write result names the added column.
func TestFollowSource_AParquetVersionWithANewColumnSaysSo(t *testing.T) {
	h := parquetHarness(t, parquetFile(t, col(t, "id", "bigint")))
	reg, err := h.reg.Register(context.Background(), testCaller(), parquetSource("artifacts/u1/asset_1/rows.parquet"),
		Request{Connection: "scratch", Source: "mcp", Follow: true})
	require.NoError(t, err)

	src := parquetSource("artifacts/u1/asset_1/v2/rows.parquet")
	h.objects.entries = append(h.objects.entries, ObjectEntry{Key: src.HeadKey})
	h.objects.body = parquetFile(t, col(t, "id", "double"), col(t, "region", "varchar"))
	h.trino.statements = nil

	out := h.reg.FollowSource(context.Background(), src, 2)
	require.Len(t, out, 1)
	assert.True(t, out[0].Followed, out[0].Reason)
	assert.Equal(t, "added region VARCHAR; id is now DOUBLE (was BIGINT)", out[0].ColumnChanges)
	assert.Contains(t, out[0].Sentence(), "Its columns changed with the file: added region VARCHAR; id is now DOUBLE")
	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Equal(t, []Column{{Name: "id", Type: "DOUBLE"}, {Name: "region", Type: "VARCHAR"}}, stored.Columns)
}

// TestFollowSource_AnInvalidParquetVersionLeavesTheTable: a new version whose
// footer the platform refuses records why and leaves the table where it was.
func TestFollowSource_AnInvalidParquetVersionLeavesTheTable(t *testing.T) {
	h := parquetHarness(t, parquetFile(t, col(t, "id", "bigint")))
	reg, err := h.reg.Register(context.Background(), testCaller(), parquetSource("artifacts/u1/asset_1/rows.parquet"),
		Request{Connection: "scratch", Source: "mcp", Follow: true})
	require.NoError(t, err)

	src := parquetSource("artifacts/u1/asset_1/v2/rows.parquet")
	h.objects.entries = append(h.objects.entries, ObjectEntry{Key: src.HeadKey})
	h.objects.body = []byte("not a parquet file at all")
	h.trino.statements = nil

	out := h.reg.FollowSource(context.Background(), src, 2)
	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Contains(t, out[0].Reason, "not a Parquet file")
	assert.Empty(t, h.trino.statements)
	stored, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	assert.Contains(t, stored.FollowError, "not a Parquet file")
	assert.Equal(t, reg.Location, stored.Location)
}

// TestFollowSource_ALegacyJSONLinesRegistrationStaysVarchar: a JSON-lines
// registration made before typed columns keeps declaring VARCHAR across a
// follow, and a nested value it could never read is refused with the way to a
// typed registration.
func TestFollowSource_ALegacyJSONLinesRegistrationStaysVarchar(t *testing.T) {
	h := jsonlHarness(t, "{\"id\":\"1\",\"note\":\"a\"}\n")
	reg, err := h.reg.Register(context.Background(), testCaller(), jsonlSource(),
		Request{Connection: "scratch", Source: "mcp", Follow: true})
	require.NoError(t, err)
	legacy, err := h.store.Get(context.Background(), reg.ID)
	require.NoError(t, err)
	legacy.AllVarchar = true
	h.store.rows[reg.ID] = *legacy

	src := jsonlSource()
	src.HeadKey = "artifacts/u1/asset_1/v2/rows.jsonl"
	h.objects.entries = append(h.objects.entries, ObjectEntry{Key: src.HeadKey})
	h.objects.body = []byte("{\"id\":1,\"note\":\"a\",\"n\":2.5}\n")
	h.trino.statements = nil

	out := h.reg.FollowSource(context.Background(), src, 2)
	require.Len(t, out, 1)
	assert.True(t, out[0].Followed, out[0].Reason)
	assert.Contains(t, h.trino.statements[2], `("id" VARCHAR, "note" VARCHAR, "n" VARCHAR)`)

	src.HeadKey = "artifacts/u1/asset_1/v3/rows.jsonl"
	h.objects.entries = append(h.objects.entries, ObjectEntry{Key: src.HeadKey})
	h.objects.body = []byte("{\"id\":1,\"tags\":[\"x\"]}\n")
	out = h.reg.FollowSource(context.Background(), src, 3)
	require.Len(t, out, 1)
	assert.False(t, out[0].Followed)
	assert.Contains(t, out[0].Reason, `the value of "tags" is a nested object or list`)
	assert.Contains(t, out[0].Reason, "register the file again under the same name")
}

func TestFormatOf_Parquet(t *testing.T) {
	assert.Equal(t, FormatParquet, formatOf(tableparquet.ContentType, "k/x.bin"))
	assert.Equal(t, FormatParquet, formatOf("application/x-parquet", "k/x"))
	assert.Equal(t, FormatParquet, formatOf("", "k/x.PARQUET"))
	assert.Equal(t, FormatParquet, formatOf("application/octet-stream", "k/x.parquet"))
	assert.Equal(t, FormatParquet, formatOf("text/plain", "k/x.parquet"),
		"a file named .parquet whose bytes are text is taken as Parquet and refused by its magic")
}

func TestColumnChanges(t *testing.T) {
	before := []Column{{Name: "a", Type: "BIGINT"}, {Name: "b", Type: "VARCHAR"}, {Name: "c", Type: "DATE"}}
	after := []Column{{Name: "c", Type: "DATE"}, {Name: "a", Type: "DOUBLE"}, {Name: "d", Type: "BOOLEAN"}}
	assert.Equal(t, "added d BOOLEAN; removed b; a is now DOUBLE (was BIGINT)", ColumnChanges(before, after))
	assert.Empty(t, ColumnChanges(before, before))
	assert.Empty(t, ColumnChanges(before, []Column{before[2], before[0], before[1]}), "a reorder is not a change")
}

// TestRangeReader_ReadsAtAnOffset pins the ReaderAt the footer is read
// through: a short read at the end is io.EOF, and the first store failure is
// kept so a refusal and a failed read are told apart.
func TestRangeReader_ReadsAtAnOffset(t *testing.T) {
	objects := &fakeObjects{body: []byte("0123456789")}
	r := &rangeReader{ctx: context.Background(), objects: objects, bucket: "b", key: "k"}

	buf := make([]byte, 4)
	n, err := r.ReadAt(buf, 2)
	require.NoError(t, err)
	assert.Equal(t, "2345", string(buf[:n]))

	n, err = r.ReadAt(buf, 8)
	assert.Equal(t, 2, n)
	assert.ErrorIs(t, err, io.EOF)

	n, err = r.ReadAt(nil, 0)
	assert.Zero(t, n)
	assert.NoError(t, err)

	objects.getErr = errors.New("connection reset")
	_, err = r.ReadAt(buf, 0)
	require.Error(t, err)
	_, _ = r.ReadAt(buf, 0)
	assert.EqualError(t, r.err, "connection reset", "the first failure is the one kept")
}

func TestSampleJoinSQL_FollowsTheFormat(t *testing.T) {
	reg := Registration{Catalog: "c", Schema: "s", Table: "t", Columns: []Column{{Name: "id", Type: "BIGINT"}}}
	for format, wantCast := range map[string]bool{FormatCSV: true, "": true, FormatJSONLines: false, FormatParquet: false} {
		reg.Format = format
		sample := SampleJoinSQL(reg)
		assert.Equal(t, wantCast, strings.Contains(sample, "CAST("), "%q: %s", format, sample)
	}
	reg.Format, reg.AllVarchar = FormatJSONLines, true
	assert.Contains(t, SampleJoinSQL(reg), "CAST(", "a JSON-lines table declared VARCHAR still casts")
	assert.Empty(t, SampleJoinSQL(Registration{}))
}
