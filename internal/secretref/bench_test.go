package secretref

import (
	"context"
	"strings"
	"testing"
)

// Every api connection answer passes through the redactor holding the secrets
// its request was filled with (#2716): a JSON body of a few kilobytes, one
// secret value in it, two secrets held.
func BenchmarkRedactAnAnswer(b *testing.B) {
	_, r := WithRedactor(context.Background())
	r.Add("stripe_key", "sk_live_51Hx3examplesecretvalue")
	r.Add("crm_token", "crm-tok-8f2a9c1e")
	row := `{"id":"cus_1042","email":"jane@example.com","balance":1200,"metadata":{"source":"web"}},`
	body := []byte(`{"data":[` + strings.Repeat(row, 40) + `{"echo":"sk_live_51Hx3examplesecretvalue"}]}`)
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for range b.N {
		_ = r.Bytes(body)
	}
}
