package pathtemplate

import "testing"

// Every api_invoke_endpoint call matches its path against the operations of
// the connection's catalog spec (#2716): templates of the shapes the api-test
// fixture's spec holds, one concrete path matched against all of them.
var benchTemplates = []string{
	"/customers", "/customers/{id}", "/customers/{id}/orders", "/customers/{id}/orders/{orderId}",
	"/orders", "/orders/{orderId}", "/orders/{orderId}/items", "/orders/{orderId}/items/{itemId}",
	"/products", "/products/{sku}", "/inventory/{warehouse}/{sku}", "/reports/{year}/{month}",
	"/webhooks", "/webhooks/{id}/deliveries", "/users/{userId}/preferences", "/health",
}

func BenchmarkMatchAgainstASpec(b *testing.B) {
	const concrete = "/customers/c-1042/orders/o-77"
	b.ReportAllocs()
	for range b.N {
		for _, tpl := range benchTemplates {
			_ = Match(concrete, tpl)
		}
	}
}
