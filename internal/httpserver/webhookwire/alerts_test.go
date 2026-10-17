package webhookwire

import (
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/txn2/mcp-data-platform/internal/webhook/whalert"
	"github.com/txn2/mcp-data-platform/internal/webhook/whsource"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// TestEnableAlertsBuildsTheEvaluation: the evaluation is built over the
// webhook stores assemble kept, and a deployment without webhooks builds none.
func TestEnableAlertsBuildsTheEvaluation(t *testing.T) {
	var none *Webhooks
	none.EnableAlerts(nil, "https://p") // no webhooks, nothing to build

	db, _, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	w := &Webhooks{alertDeps: whalert.Deps{
		DB: db, Settings: whalert.NewSettingsStore(db), Reader: whstore.New(db), Sources: whsource.NewStore(db, nil),
	}}
	w.EnableAlerts(nil, "https://p/")
	assert.NotNil(t, w.alerts)

	empty := &Webhooks{}
	empty.EnableAlerts(nil, "")
	assert.Nil(t, empty.alerts, "with nothing to read there is no evaluation")
}

func TestAlertInterval(t *testing.T) {
	t.Setenv(EnvAlertInterval, "")
	assert.Zero(t, alertInterval())
	t.Setenv(EnvAlertInterval, "10s")
	assert.Equal(t, 10*time.Second, alertInterval())
	t.Setenv(EnvAlertInterval, "often")
	assert.Zero(t, alertInterval())
}
