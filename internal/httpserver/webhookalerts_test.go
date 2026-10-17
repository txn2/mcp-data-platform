package httpserver

import "testing"

// TestEnableWebhookAlertsWithoutWebhooks: a deployment without webhooks, or
// without a platform, builds no alerts and does not fail.
func TestEnableWebhookAlertsWithoutWebhooks(_ *testing.T) {
	enableWebhookAlerts(nil, nil, nil)
}
