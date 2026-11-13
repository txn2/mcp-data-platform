package scripthandler

import (
	"context"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptevents"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// Scripts reads a script by id, nil and nil for none.
type Scripts interface {
	GetByID(ctx context.Context, id string) (*script.Script, error)
}

// Reader reads the script_handlers view (View).
type Reader interface {
	List(ctx context.Context, f Filter) ([]Handler, int, error)
	Get(ctx context.Context, id string) (Handler, error)
	TypesOf(ctx context.Context, scriptIDs []string) (map[string][]string, error)
}

// Webhooks keeps webhook handlers (scriptevents.PostgresStore).
type Webhooks interface {
	AddHandler(ctx context.Context, scriptID, source, actor string) (scriptevents.Handler, bool, error)
	HandlerByID(ctx context.Context, id string) (scriptevents.Handler, error)
	Handlers(ctx context.Context, filter scriptevents.HandlerFilter) ([]scriptevents.Handler, error)
	EnableHandler(ctx context.Context, id string, enabled bool, actor string) error
	DeleteHandler(ctx context.Context, id string) error
}

// ScheduleStore keeps schedule handlers (scriptstore.Store): the schedule
// contract and the delete of one that produced no runs.
type ScheduleStore interface {
	script.ScheduleStore
	DeleteSchedule(ctx context.Context, id string) error
}
