package apigateway

import "testing"

// TestUpstreamCarriesTheDeliveryIDHeader holds that the transport a
// notification channel delivers through names an hmac connection's id header,
// so a JSON delivery's id is sent in the header the signature covers (#1997).
func TestUpstreamCarriesTheDeliveryIDHeader(t *testing.T) {
	tk := NewMulti(MultiConfig{})
	if err := tk.AddConnection("hook", map[string]any{
		"base_url": "https://receiver.example.com", "auth_mode": "hmac", "credential": "s",
		"hmac_preset": "standard_webhooks",
	}); err != nil {
		t.Fatalf("AddConnection: %v", err)
	}
	up, err := tk.Upstream("hook")
	if err != nil {
		t.Fatalf("Upstream: %v", err)
	}
	if got := up.DeliveryIDHeader(); got != "webhook-id" {
		t.Errorf("DeliveryIDHeader = %q, want the preset's webhook-id", got)
	}
}
