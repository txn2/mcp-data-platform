package portal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A platform catalog citation is labeled by its table, its column, or the key
// a definition reference carries, unless the batch resolved a name (#1923).
func TestPlatformCatalogLabel(t *testing.T) {
	assert.Equal(t, "orders", platformCatalogLabel("mcp:dataset:(trino,acme,hive.sales.orders)", nil))
	assert.Equal(t, "orders.amount", platformCatalogLabel("mcp:column:(trino,acme,hive.sales.orders,amount)", nil))
	assert.Equal(t, "net-revenue", platformCatalogLabel("mcp:glossary_term:net-revenue", nil))
	assert.Equal(t, "Net revenue", platformCatalogLabel("mcp:glossary_term:net-revenue",
		map[string]string{"mcp:glossary_term:net-revenue": "Net revenue"}))
	assert.Equal(t, "garbage", platformCatalogLabel("garbage", nil))
}
