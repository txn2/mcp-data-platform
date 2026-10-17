package settingsapi

import (
	"context"
	"fmt"
	"net/http"

	"github.com/txn2/mcp-data-platform/internal/webhook/whalert"
)

// webhookAlertPath is the webhook receiver alerts' settings (#2149).
const webhookAlertPath = "/api/v1/admin/settings/webhook-alert"

// registerWebhookAlert mounts the webhook alert settings routes.
func registerWebhookAlert(mux *http.ServeMux, h *handler) {
	registerAlertSettings(mux, h, webhookAlertPath, h.cfg.WebhookAlert != nil, alertHandlers{get: h.getWebhookAlert, set: h.setWebhookAlert})
}

// getWebhookAlert handles GET /api/v1/admin/settings/webhook-alert.
//
// @Summary      Get webhook alert settings
// @Description  Returns the webhook receiver alerts: the notification channels they are posted to and each condition's warning and critical thresholds. Per replica: spool fill and oldest event age as a percent of the spool's max_bytes and max_age, drain failures in a row (and minutes failing, which is critical), seconds a replica holding events may go without reporting, and the spool volume's free percent. Per source, as defaults and per-source overrides: the minutes refusals are judged over, the unauthorized percent over 15 minutes, the acknowledgement p99 in milliseconds over 5 minutes, minutes the oldest owed compaction is past its window and grace, and minutes of silence (off unless set). A zero threshold is off.
// @Tags         Settings
// @Produce      json
// @Success      200  {object}  whalert.View
// @Failure      500  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/webhook-alert [get]
func (h *handler) getWebhookAlert(w http.ResponseWriter, r *http.Request) {
	getAlertSettings(w, r, "webhook alert", h.readWebhookAlert)
}

// setWebhookAlert handles PUT /api/v1/admin/settings/webhook-alert.
//
// @Summary      Update webhook alert settings
// @Description  Writes the webhook alert settings. A field left out keeps its stored value; sources, when present, replaces every source's overrides. A threshold's warning must be short of its critical, percentages are 0-100, and 0 turns a threshold off. A channel name no channel is stored under is refused.
// @Tags         Settings
// @Accept       json
// @Produce      json
// @Param        request  body  whalert.Input  true  "Webhook alert settings"
// @Success      200  {object}  whalert.View
// @Failure      400  {object}  problemDetail
// @Security     ApiKeyAuth
// @Security     BearerAuth
// @Router       /admin/settings/webhook-alert [put]
func (h *handler) setWebhookAlert(w http.ResponseWriter, r *http.Request) {
	putAlertSettings[whalert.Settings, whalert.View, whalert.Input](h, w, r, alertSection[whalert.Settings, whalert.View]{
		what: "webhook alert", read: h.readWebhookAlert, set: h.cfg.WebhookAlert.Set,
		settings: func(v whalert.View) whalert.Settings { return v.Settings },
		channels: func(s whalert.Settings) []string { return s.Channels },
		view: func(s whalert.Settings, author, at string) whalert.View {
			return whalert.View{Settings: s, UpdatedBy: author, UpdatedAt: at}
		},
	})
}

// readWebhookAlert is the stored webhook alert settings, or the defaults.
func (h *handler) readWebhookAlert(ctx context.Context) (whalert.View, error) {
	v, err := whalert.Read(ctx, h.cfg.WebhookAlert)
	if err != nil {
		return v, fmt.Errorf("webhook alert settings: %w", err)
	}
	return v, nil
}
