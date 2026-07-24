package apigen

// The gold surface: the ten operations tasks require, byte-identical in
// every tier. The world behind them is bench/internal/gen's fixed-seed
// dataset (customers with region/tier/created_at, orders with status and
// integer-cent amounts), giving the study continuity with report 1.

// centsDesc documents the integer-cent convention wherever an amount
// appears. Description quality is a measured confound (see the design
// doc); every field and parameter description in the catalog follows the
// same one-sentence template style, and units are stated wherever they
// exist.
const centsDesc = "integer US cents"

// customerFields is the customer object schema.
func customerFields() []Field {
	return []Field{
		{Name: "id", Type: "integer", Desc: "Unique customer identifier"},
		{Name: "name", Type: "string", Desc: "Customer full name"},
		{Name: "region", Type: "string", Desc: "Sales region: North, South, East, or West"},
		{Name: "tier", Type: "string", Desc: "Subscription tier: basic, plus, or enterprise"},
		{Name: "created_at", Type: "date-time", Desc: "Account creation timestamp, UTC"},
	}
}

// orderFields is the order object schema.
func orderFields() []Field {
	return []Field{
		{Name: "id", Type: "integer", Desc: "Unique order identifier"},
		{Name: "customer_id", Type: "integer", Desc: "Identifier of the customer who placed the order"},
		{Name: "placed_at", Type: "date-time", Desc: "Order placement timestamp, UTC"},
		{Name: "status", Type: "string", Desc: "Order status: pending, completed, refunded, or canceled"},
		{Name: "amount", Type: "integer", Desc: "Order amount in " + centsDesc},
		{Name: "discount", Type: "integer", Desc: "Discount applied in " + centsDesc},
	}
}

// pagingParams are the shared cursor-pagination parameters. List responses
// carry no total count, so counting through a filter requires consuming
// every page.
func pagingParams() []Param {
	return []Param{
		{Name: "cursor", In: "query", Type: "string", Desc: "Opaque pagination cursor from a previous response"},
		{Name: "page_size", In: "query", Type: "integer", Desc: "Results per page, 1 to 100 (default 20)"},
	}
}

// idPathParam is the shared {id} path parameter.
func idPathParam(desc string) Param {
	return Param{Name: "id", In: "path", Type: "integer", Desc: desc, Required: true}
}

// goldOperations builds the gold operation set.
func goldOperations() []Operation {
	ops := goldCustomerOperations()
	return append(ops, goldOrderOperations()...)
}

// goldCustomerOperations builds the customer-side gold operations.
func goldCustomerOperations() []Operation {
	return []Operation{
		{
			ID: "list_customers", Kind: KindList, Method: "get", Path: "/crm/customers",
			Summary: "List customers with optional filters and cursor pagination.",
			Tag:     "crm", Gold: true,
			Params: append([]Param{
				{Name: "region", In: "query", Type: "string", Desc: "Filter by sales region: North, South, East, or West"},
				{Name: "tier", In: "query", Type: "string", Desc: "Filter by subscription tier: basic, plus, or enterprise"},
				{Name: "name", In: "query", Type: "string", Desc: "Filter by exact customer full name"},
				{Name: "created_after", In: "query", Type: "string", Desc: "Only customers created at or after this ISO 8601 timestamp"},
				{Name: "created_before", In: "query", Type: "string", Desc: "Only customers created before this ISO 8601 timestamp"},
			}, pagingParams()...),
			Response: customerFields(),
		},
		{
			ID: "get_customer", Kind: KindGet, Method: "get", Path: "/crm/customers/{id}",
			Summary: "Fetch one customer by id.",
			Tag:     "crm", Gold: true,
			Params:   []Param{idPathParam("Customer identifier")},
			Response: customerFields(),
		},
		{
			ID: "aggregate_customers", Kind: KindAggregate, Method: "get", Path: "/crm/customers:aggregate",
			Summary: "Count customers grouped by a dimension.",
			Tag:     "crm", Gold: true,
			Params: []Param{
				{Name: "group_by", In: "query", Type: "string", Desc: "Dimension to group by: region or tier", Required: true},
			},
			Response: []Field{
				{Name: "key", Type: "string", Desc: "Group value"},
				{Name: "count", Type: "integer", Desc: "Number of customers in the group"},
			},
		},
		{
			ID: "update_customer", Kind: KindUpdate, Method: "patch", Path: "/crm/customers/{id}",
			Summary: "Update a customer's region or tier.",
			Tag:     "crm", Gold: true,
			Params: []Param{idPathParam("Customer identifier")},
			Request: []Field{
				{Name: "region", Type: "string", Desc: "New sales region: North, South, East, or West"},
				{Name: "tier", Type: "string", Desc: "New subscription tier: basic, plus, or enterprise"},
			},
			Response: customerFields(),
		},
		{
			ID: "list_customer_orders", Kind: KindList, Method: "get", Path: "/crm/customers/{id}/orders",
			Summary: "List one customer's orders with cursor pagination.",
			Tag:     "crm", Gold: true,
			Params:   append([]Param{idPathParam("Customer identifier")}, pagingParams()...),
			Response: orderFields(),
		},
	}
}

// goldOrderOperations builds the order-side gold operations.
func goldOrderOperations() []Operation {
	return []Operation{
		{
			ID: "list_orders", Kind: KindList, Method: "get", Path: "/commerce/orders",
			Summary: "List orders with optional filters and cursor pagination.",
			Tag:     "commerce", Gold: true,
			Params: append([]Param{
				{Name: "status", In: "query", Type: "string", Desc: "Filter by order status: pending, completed, refunded, or canceled"},
				{Name: "customer_id", In: "query", Type: "integer", Desc: "Filter by the customer who placed the order"},
				{Name: "min_amount", In: "query", Type: "integer", Desc: "Only orders with amount at or above this value, in " + centsDesc},
				{Name: "max_amount", In: "query", Type: "integer", Desc: "Only orders with amount at or below this value, in " + centsDesc},
				{Name: "placed_after", In: "query", Type: "string", Desc: "Only orders placed at or after this ISO 8601 timestamp"},
				{Name: "placed_before", In: "query", Type: "string", Desc: "Only orders placed before this ISO 8601 timestamp"},
			}, pagingParams()...),
			Response: orderFields(),
		},
		{
			ID: "get_order", Kind: KindGet, Method: "get", Path: "/commerce/orders/{id}",
			Summary: "Fetch one order by id.",
			Tag:     "commerce", Gold: true,
			Params:   []Param{idPathParam("Order identifier")},
			Response: orderFields(),
		},
		{
			ID: "aggregate_orders", Kind: KindAggregate, Method: "get", Path: "/commerce/orders:aggregate",
			Summary: "Aggregate all orders grouped by a dimension. No filters: use list_orders to count within a filter.",
			Tag:     "commerce", Gold: true,
			Params: []Param{
				{Name: "group_by", In: "query", Type: "string", Desc: "Dimension to group by: status or month (YYYY-MM of placed_at)", Required: true},
				{Name: "metric", In: "query", Type: "string", Desc: "Aggregate to compute per group: count (default) or amount_sum in " + centsDesc},
			},
			Response: []Field{
				{Name: "key", Type: "string", Desc: "Group value"},
				{Name: "value", Type: "integer", Desc: "Aggregate value for the group"},
			},
		},
		{
			ID: "create_order", Kind: KindCreate, Method: "post", Path: "/commerce/orders",
			Summary: "Create a new order in pending status.",
			Tag:     "commerce", Gold: true,
			Request: []Field{
				{Name: "customer_id", Type: "integer", Desc: "Identifier of the customer placing the order"},
				{Name: "amount", Type: "integer", Desc: "Order amount in " + centsDesc},
			},
			Response: orderFields(),
		},
		{
			ID: "cancel_order", Kind: KindCancel, Method: "post", Path: "/commerce/orders/{id}:cancel",
			Summary: "Cancel a pending order. Orders in any other status cannot be canceled.",
			Tag:     "commerce", Gold: true,
			Params:   []Param{idPathParam("Order identifier")},
			Response: orderFields(),
		},
	}
}

// deprecatedOrdersV1 is the deprecated near-miss listing: same shape as
// list_orders, retired path, present from tier 0 so even the smallest
// catalog carries a deprecation decoy.
func deprecatedOrdersV1() Operation {
	return Operation{
		ID: "list_orders_v1", Kind: KindList, Method: "get", Path: "/commerce/v1/orders",
		Summary: "Deprecated. Use list_orders (/commerce/orders) instead.",
		Tag:     "commerce", Deprecated: true,
		Params:   pagingParams(),
		Response: orderFields(),
	}
}
