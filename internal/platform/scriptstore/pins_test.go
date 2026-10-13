package scriptstore

import (
	"context"
	"errors"
	"regexp"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// TestCreate_WritesTheResourcePinsTheSaveResolved is #2113's store half: the
// version a save creates records the file versions its loads read.
func TestCreate_WritesTheResourcePinsTheSaveResolved(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow("script_1", rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_resource_pins")).
		WithArgs("script_1", 1, "res:global/flows/page.js", "res_1", 3).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	sc := &script.Script{
		Name: "flow", Source: "print(1)", OwnerEmail: "j@example.com",
		ResourcePins: map[string]script.ResourcePin{"res:global/flows/page.js": {ResourceID: "res_1", Version: 3}},
	}
	require.NoError(t, s.Create(context.Background(), sc, testAuthor))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCreate_APinThatCannotBeWrittenRollsTheSaveBack(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow("script_1", rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_resource_pins")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	sc := &script.Script{Name: "flow", ResourcePins: map[string]script.ResourcePin{"m": {ResourceID: "r", Version: 1}}}
	assert.Error(t, s.Create(context.Background(), sc, testAuthor))

	s, mock = newMock(t)
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("INSERT INTO scripts")).
		WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at"}).AddRow("script_1", rowTime, rowTime))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_versions")).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO script_resource_pins")).WillReturnError(errors.New("boom"))
	mock.ExpectRollback()
	assert.Error(t, s.Create(context.Background(), &script.Script{Name: "carry"}, testAuthor), "a failed carry is a failed save")
}

func TestResourcePinsReadsAVersionsPins(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT module, resource_id, resource_version")).WithArgs("ver_1").
		WillReturnRows(sqlmock.NewRows([]string{"module", "resource_id", "resource_version"}).
			AddRow("res:global/flows/page.js", "res_1", 3))
	pins, err := s.ResourcePins(context.Background(), "ver_1")
	require.NoError(t, err)
	assert.Equal(t, map[string]script.ResourcePin{"res:global/flows/page.js": {ResourceID: "res_1", Version: 3}}, pins)

	mock.ExpectQuery(regexp.QuoteMeta("SELECT module")).WillReturnError(errors.New("down"))
	_, err = s.ResourcePins(context.Background(), "ver_1")
	assert.Error(t, err)
	mock.ExpectQuery(regexp.QuoteMeta("SELECT module")).
		WillReturnRows(sqlmock.NewRows([]string{"module", "resource_id", "resource_version"}).AddRow("m", "r", "not a number"))
	_, err = s.ResourcePins(context.Background(), "ver_1")
	assert.Error(t, err)
}
