package collectionsweep

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/portal/access"
	"github.com/txn2/mcp-data-platform/internal/portal/portaldomain"
)

var errStore = errors.New("store down")

type fakePeople struct{}

func (fakePeople) Person(_ context.Context, userID, email string) (user *access.User, persona string) {
	user = &access.User{UserID: userID, Email: email}
	if userID == "root" {
		user.Roles = []string{"admin"}
	}
	return user, "persona-" + userID
}

// fakeAccess opens an asset to its owner, to the people direct names, and
// fails for an asset named in fail.
type fakeAccess struct {
	direct map[string]bool // userID/assetID
	fail   map[string]bool // assetID
}

func (fakeAccess) IsAdmin(u *access.User) bool { return len(u.Roles) > 0 }

func (f fakeAccess) DirectViewGrant(ctx context.Context, assetID string, asset *portaldomain.Asset, u *access.User) (bool, error) {
	if portaldomain.CallerPersona(ctx) != "persona-"+u.UserID {
		return false, errors.New("the owner's persona was not on the context")
	}
	if f.fail[assetID] {
		return false, errStore
	}
	return access.OwnsAsset(asset, u) || f.direct[u.UserID+"/"+assetID], nil
}

type fakeGrants map[string][]string // userID/assetID -> collections

func (f fakeGrants) CollectionsGrantingAsset(_ context.Context, assetID, userID, _ string) ([]string, error) {
	if assetID == "grant-fails" {
		return nil, errStore
	}
	return f[userID+"/"+assetID], nil
}

var itemCols = []string{"c", "a", "o", "oe", "ao", "ae"}

func TestNew_NeedsEveryDependency(t *testing.T) {
	db, _, err := sqlmock.New()
	require.NoError(t, err)
	assert.Nil(t, New(nil, fakePeople{}, fakeAccess{}, fakeGrants{}))
	assert.Nil(t, New(db, nil, fakeAccess{}, fakeGrants{}))
	assert.Nil(t, New(db, fakePeople{}, nil, fakeGrants{}))
	assert.Nil(t, New(db, fakePeople{}, fakeAccess{}, nil))
	assert.NotNil(t, New(db, fakePeople{}, fakeAccess{}, fakeGrants{}))
}

func TestRun_RemovesOnlyWhatNoGrantReaches(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	s := New(db, fakePeople{}, fakeAccess{direct: map[string]bool{"alice/shared": true, "bob/chained": true}, fail: map[string]bool{"broken": true}},
		fakeGrants{"alice/chained": {"c-bob"}, "carol/circled": {"c-dave"}, "dave/circled": {"c-carol"}})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT c.id")).WillReturnRows(sqlmock.NewRows(itemCols).
		AddRow("c-alice", "mine", "alice", "", "alice", "").
		AddRow("c-alice", "shared", "alice", "", "zed", "").
		AddRow("c-alice", "private", "alice", "", "zed", "").
		AddRow("c-alice", "chained", "alice", "", "zed", "").
		AddRow("c-alice", "broken", "alice", "", "zed", "").
		AddRow("c-alice", "grant-fails", "alice", "", "zed", "").
		AddRow("c-bob", "chained", "bob", "", "zed", "").
		AddRow("c-carol", "circled", "carol", "", "zed", "").
		AddRow("c-dave", "circled", "dave", "", "zed", "").
		AddRow("c-root", "private", "root", "", "zed", ""))
	for _, p := range [][2]string{{"c-alice", "private"}, {"c-carol", "circled"}, {"c-dave", "circled"}} {
		mock.ExpectExec(regexp.QuoteMeta("WITH gone AS")).WithArgs(p[0], p[1]).WillReturnResult(sqlmock.NewResult(0, 1))
	}
	removed, err := s.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, int64(3), removed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestRun_ARemovalAlreadyGoneIsNotCounted(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	s := New(db, fakePeople{}, fakeAccess{}, fakeGrants{})
	mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT c.id")).WillReturnRows(sqlmock.NewRows(itemCols).AddRow("c", "x", "alice", "", "zed", ""))
	mock.ExpectExec(regexp.QuoteMeta("WITH gone AS")).WillReturnResult(sqlmock.NewResult(0, 0))
	removed, err := s.Run(context.Background())
	require.NoError(t, err)
	assert.Zero(t, removed)
}

func TestRun_StoreFailures(t *testing.T) {
	ctx := context.Background()
	t.Run("listing", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT c.id")).WillReturnError(errStore)
		_, err = New(db, fakePeople{}, fakeAccess{}, fakeGrants{}).Run(ctx)
		require.ErrorContains(t, err, "listing collection items")
	})
	t.Run("scanning", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT c.id")).WillReturnRows(sqlmock.NewRows([]string{"c"}).AddRow("c"))
		_, err = New(db, fakePeople{}, fakeAccess{}, fakeGrants{}).Run(ctx)
		require.ErrorContains(t, err, "scanning a collection item")
	})
	t.Run("reading", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT c.id")).WillReturnRows(sqlmock.NewRows(itemCols).
			AddRow("c", "x", "alice", "", "zed", "").RowError(0, errStore))
		_, err = New(db, fakePeople{}, fakeAccess{}, fakeGrants{}).Run(ctx)
		require.ErrorContains(t, err, "reading collection items")
	})
	t.Run("removing", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		mock.ExpectQuery(regexp.QuoteMeta("SELECT DISTINCT c.id")).WillReturnRows(sqlmock.NewRows(itemCols).AddRow("c", "x", "alice", "", "zed", ""))
		mock.ExpectExec(regexp.QuoteMeta("WITH gone AS")).WillReturnError(errStore)
		_, err = New(db, fakePeople{}, fakeAccess{}, fakeGrants{}).Run(ctx)
		require.ErrorContains(t, err, "removing a collection item")
	})
}
