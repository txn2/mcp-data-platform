package notifyapi

import (
	"encoding/json"
	"testing"

	"github.com/txn2/mcp-data-platform/pkg/notification"
)

func TestListNotifications_AChannelRowNamesItsChannelAndWhatItHeldBack(t *testing.T) {
	doc := notification.Document{Title: "Feed stale", Suppressed: 4}
	row := notification.Notification{
		ID: 9, Recipient: "channel:ops", Channel: "ops", Category: notification.CategoryChannel,
		Payload: notification.Payload{Kind: notification.KindChannel, ItemTitle: "Feed stale", Document: &doc},
	}
	store := &fakeHistory{rows: []notification.Notification{row, failedRow()}, total: 2}
	w := serve(Config{History: store}, "/api/v1/admin/notifications?channel=ops")

	if store.lastList.Channel != "ops" || store.lastCount.Channel != "ops" {
		t.Errorf("the channel filter did not reach the store: list %+v count %+v", store.lastList, store.lastCount)
	}
	var resp notificationListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if got := resp.Data[0]; got.Channel != "ops" || got.Suppressed != 4 {
		t.Errorf("channel row = %+v", got)
	}
	if got := resp.Data[1]; got.Channel != "" || got.Suppressed != 0 {
		t.Errorf("a person's row = %+v, want no channel and nothing suppressed", got)
	}
}

func TestNotificationStats_HonorTheChannelFilter(t *testing.T) {
	store := &fakeHistory{counts: map[string]int{}}
	serve(Config{History: store}, "/api/v1/admin/notifications/stats?channel=ops")
	if store.lastCounts.Channel != "ops" {
		t.Errorf("stats filter = %+v", store.lastCounts)
	}
}
