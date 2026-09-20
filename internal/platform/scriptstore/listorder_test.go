package scriptstore

import (
	"context"
	"regexp"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/pkg/script"
)

// The listing is ordered and counted in the STORE (#1795). Both halves matter
// for the same reason: the store applies the page cap, so a column sorted
// anywhere else sorts the page, and a total counted anywhere else counts the
// page. These hold the SQL that makes each true.

// TestOrderBy_DefaultsToMostRecentlyUpdated pins the ordering a filter that
// names no column gets. It is the one every caller had before ordering
// existed, and Desc is deliberately not consulted: a filter that names nothing
// must not land on ascending because a bool sat at its zero value.
func TestOrderBy_DefaultsToMostRecentlyUpdated(t *testing.T) {
	assert.Equal(t, "ORDER BY updated_at DESC, id DESC", orderBy(script.ListFilter{}))
	assert.Equal(t, "ORDER BY updated_at DESC, id DESC",
		orderBy(script.ListFilter{Desc: false}),
		"an unset column ignores the direction rather than flipping the default")
}

// TestOrderBy_NamesTheColumnAndTheDirection covers both directions of a named
// column, and the id tie-breaker that goes with it: none of the sortable
// columns is unique, so without it two scripts sharing a value can swap places
// between two reads of the same listing.
func TestOrderBy_NamesTheColumnAndTheDirection(t *testing.T) {
	for _, tc := range []struct {
		name string
		sort script.SortColumn
		desc bool
		want string
	}{
		{"name ascending", script.SortName, false, "ORDER BY name ASC, id ASC"},
		{"name descending", script.SortName, true, "ORDER BY name DESC, id DESC"},
		{"display name", script.SortDisplayName, false, "ORDER BY display_name ASC, id ASC"},
		{"owner", script.SortOwnerEmail, false, "ORDER BY owner_email ASC, id ASC"},
		{"created", script.SortCreatedAt, true, "ORDER BY created_at DESC, id DESC"},
		{"updated", script.SortUpdatedAt, false, "ORDER BY updated_at ASC, id ASC"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, orderBy(script.ListFilter{Sort: tc.sort, Desc: tc.desc}))
		})
	}
}

// TestBuildListQuery_OrdersAheadOfTheLimit is the invariant the whole feature
// rests on: ORDER BY must precede LIMIT, or the cap chooses the rows and the
// ordering only arranges the ones it chose.
func TestBuildListQuery_OrdersAheadOfTheLimit(t *testing.T) {
	query, args := buildListQuery(script.ListFilter{Sort: script.SortName, Limit: 10})

	order := strings.Index(query, "ORDER BY")
	limit := strings.Index(query, "LIMIT")
	require.NotEqual(t, -1, order, "the listing must carry an ordering")
	require.NotEqual(t, -1, limit, "the listing must carry a limit")
	assert.Less(t, order, limit, "ORDER BY must come before LIMIT")
	assert.Equal(t, 10, args[len(args)-1], "the limit is the last bound argument")
}

// TestBuildListQuery_ColumnIsNeverInterpolatedFromACaller proves the whitelist
// is the only way a column reaches the SQL: ParseSortColumn REPORTS an
// unrecognized value rather than rendering it, and a route that is told so
// leaves the filter's Sort empty.
func TestBuildListQuery_ColumnIsNeverInterpolatedFromACaller(t *testing.T) {
	col, ok := script.ParseSortColumn("updated_at; DROP TABLE scripts")
	assert.False(t, ok, "an unrecognized column is reported, not substituted")

	// What a route does with that: leave Sort empty, which is the store's own
	// default ordering rather than the named column at the caller's direction.
	filter := script.ListFilter{}
	if ok {
		filter.Sort = col
	}
	query, _ := buildListQuery(filter)
	assert.NotContains(t, query, "DROP TABLE")
	assert.Contains(t, query, "ORDER BY updated_at DESC")
}

// TestBuildCountQuery_SharesThePredicateAndDropsTheLimit holds the count to the
// listing's own predicate. A total assembled a second way is a total that can
// disagree with the rows it describes.
func TestBuildCountQuery_SharesThePredicateAndDropsTheLimit(t *testing.T) {
	filter := script.ListFilter{OwnerEmail: "jane@example.com", Category: "reports", Limit: 5}

	listQ, listArgs := buildListQuery(filter)
	countQ, countArgs := buildCountQuery(filter)

	assert.Contains(t, countQ, "SELECT COUNT(*) FROM scripts")
	assert.NotContains(t, countQ, "LIMIT", "a count that carried the cap would count the page")
	for _, clause := range []string{"owner_email = $1", "category = $2"} {
		assert.Contains(t, listQ, clause)
		assert.Contains(t, countQ, clause, "the count must apply the listing's predicate")
	}
	// The listing binds the limit as its last argument; the count binds the
	// predicate's arguments and nothing else.
	assert.Equal(t, listArgs[:len(listArgs)-1], countArgs)
}

// TestBuildScheduledCountQuery_AsksOnlyForTheOnesWithACadence covers the
// "scheduled" half of the health line.
func TestBuildScheduledCountQuery_AsksOnlyForTheOnesWithACadence(t *testing.T) {
	query, args := buildScheduledCountQuery(script.ListFilter{OwnerEmail: "jane@example.com"})

	assert.Contains(t, query, "SELECT COUNT(*) FROM scripts")
	assert.Contains(t, query, "EXISTS (SELECT 1 FROM script_schedules")
	assert.Contains(t, query, "script_schedules.script_id = scripts.id")
	assert.Contains(t, query, "owner_email = $1", "the predicate still applies")
	assert.NotContains(t, query, "LIMIT")
	assert.Equal(t, []any{"jane@example.com"}, args)
}

// TestCount_ReturnsTheTotal and its scheduled counterpart run the two methods
// against a mocked database, so the scan as well as the SQL is covered.
func TestCount_ReturnsTheTotal(t *testing.T) {
	s, mock := newMock(t)
	filter := script.ListFilter{OwnerEmail: "jane@example.com"}
	want, _ := buildCountQuery(filter)
	mock.ExpectQuery(regexp.QuoteMeta(want)).
		WithArgs("jane@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(240))

	got, err := s.Count(context.Background(), filter)

	require.NoError(t, err)
	assert.Equal(t, 240, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCount_ReportsAFailedRead(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(assert.AnError)

	_, err := s.Count(context.Background(), script.ListFilter{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "count scripts")
}

func TestCountScheduled_CountsTheOnesWithACadence(t *testing.T) {
	s, mock := newMock(t)
	filter := script.ListFilter{OwnerEmail: "jane@example.com"}
	want, _ := buildScheduledCountQuery(filter)
	mock.ExpectQuery(regexp.QuoteMeta(want)).
		WithArgs("jane@example.com").
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(96))

	got, err := s.CountScheduled(context.Background(), filter)

	require.NoError(t, err)
	assert.Equal(t, 96, got)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestCountScheduled_ReportsAFailedRead(t *testing.T) {
	s, mock := newMock(t)
	mock.ExpectQuery("SELECT COUNT").WillReturnError(assert.AnError)

	_, err := s.CountScheduled(context.Background(), script.ListFilter{})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "count scheduled scripts")
}
