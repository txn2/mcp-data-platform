// Package scriptexamples holds the built-in worked scripts manage_script
// offers an author (command=help lists them, get returns one by name). Every
// one is a whole script in the shape the authoring gates hold a new script to
// (#1913): its work in main(), each function documented, in the canonical
// format; a test in internal/platform/scriptlayer holds them to it.
package scriptexamples

import "github.com/txn2/mcp-data-platform/pkg/script"

// Example is one built-in worked script.
type Example struct {
	Name        string
	Description string
	Source      string
	// Params are the parameters the script declares, which create takes
	// with the source; a handler's args bind to them.
	Params []script.Param
}

// Fields renders the example as manage_script get answers it. It is marked
// builtin so nobody mistakes it for a stored script and tries to patch it.
func (e Example) Fields() map[string]any {
	return map[string]any{
		"name": e.Name, "description": e.Description, "source": e.Source, "params": nonNilParams(e.Params),
		"builtin": true,
		"message": "This is a built-in worked example, not a stored script. Copy it into create and edit from there.",
	}
}

func nonNilParams(p []script.Param) []script.Param {
	if p == nil {
		return []script.Param{}
	}
	return p
}

// ReferenceName is the complete reference script: every idiom a saved
// automation is built from, and the tests it is saved with (#1939). The
// built-in knowledge page platform-reference-script shows it verbatim.
const ReferenceName = "example-weekly-revenue"

// All is the seeded worked scripts. Six, not ten: they exist to show
// the shape of a script and the idioms every job needs — a date derived from
// the pinned fire time, a bound parameter, and a watermark carried in the
// script's state — not to be a cookbook that invites copying without reading.
var All = []Example{
	{
		Name:        "example-daily-sales",
		Description: "A daily report: derive yesterday from the pinned fire time, query one bound parameter, export the rows.",
		Source: `# A daily sales report. Every date comes from the run's pinned fire time,
# so re-running this months later reproduces exactly what it said.

SALES_BY_REGION = """
    SELECT region, sum(amount) AS total, count(*) AS orders
      FROM sales.orders
     WHERE order_date = DATE :day
     GROUP BY region
     ORDER BY region
"""

def main():
    """Exports yesterday's sales by region."""
    report_date = date.add_days(date.of(run.fire_time), -1)
    print("reporting on " + report_date)

    result = platform.query(
        connection = "primary",
        sql = SALES_BY_REGION,
        params = {"day": report_date},
    )

    rows = result["rows"]
    print("regions: %d" % len(rows))
    for row in rows:
        print("%s %s" % (row["region"], row["total"]))

    platform.export(
        name = "daily-sales-" + report_date,
        rows = rows,
        format = "csv",
    )

def test_daily_sales():
    """The recorded run exports each region's total under yesterday's date."""

    # The recording is the id run_draft returned for a draft of this script.
    testing.replay("dpx_recording_of_a_draft")
    main()
    out = testing.outputs().exports[0]
    assert.eq(out.name, "daily-sales-" + date.add_days(date.of(run.fire_time), -1))

    # Every column the export carries is asserted on: a save refuses a test
    # that leaves one unread.
    assert.eq(out.rows, [
        {"region": "east", "total": 1200, "orders": 14},
        {"region": "west", "total": 800, "orders": 9},
    ])
`,
	},
	{
		Name:        "example-region-rollup",
		Description: "A parameterized rollup: a declared enum and a bound list, with the empty case handled instead of raised.",
		Source: `# A month-to-date rollup for a set of regions. Declare the script's params as
# {"name": "regions", "type": "list", "items": "string", "label": "Regions"} and
# {"name": "grain", "type": "enum", "values": ["region", "channel"],
#  "required": True}. A list parameter checks every element when the run is
# bound and reaches the script as a list, which platform.query binds as IN (...).

TOTALS = """
    SELECT region, channel, sum(amount) AS total
      FROM sales.orders
     WHERE order_date >= DATE :start AND order_date <= DATE :end
       AND region IN :regions
     GROUP BY region, channel
"""

def rollup(rows, grain):
    """Adds up the totals of the rows that share a grain value."""
    totals = {}
    for row in rows:
        key = row[grain]
        totals[key] = totals.get(key, 0) + row["total"]
    return [{"key": k, "total": totals[k]} for k in sorted(totals)]

def main():
    """Exports this month's totals for the chosen regions."""
    today = date.of(run.fire_time)
    regions = run.params["regions"]
    if not regions:
        # There is no try/except: stop deliberately, with a message the run
        # record will carry.
        fail("no regions were supplied")

    result = platform.query(
        connection = "primary",
        sql = TOTALS,
        params = {"start": date.start_of_month(today), "end": today, "regions": regions},
    )
    summary = rollup(result["rows"], run.params["grain"])
    print(json.encode(summary))
    platform.export(name = "region-rollup", rows = summary, format = "json")

def test_rollup_adds_up_each_grain():
    """Rows that share a region add up to one total."""
    rows = [
        {"region": "east", "total": 2},
        {"region": "east", "total": 3},
        {"region": "west", "total": 1},
    ]
    assert.eq(rollup(rows, "region"), [{"key": "east", "total": 5}, {"key": "west", "total": 1}])

def test_recorded_rollup():
    """The recorded run exports the month's totals for the chosen regions."""
    testing.replay("dpx_recording_of_a_draft")
    main()
    out = testing.outputs().exports[0]
    assert.eq(out.rows, [{"key": "east", "total": 3400}, {"key": "west", "total": 2000}])
`,
	},
	{
		Name:        "example-incremental-sync",
		Description: "An incremental job: read the watermark from the script's state, pull what changed since it, export, and save the new watermark.",
		Source: `# An incremental pull. The window starts where the last SUCCESSFUL run
# stopped, read from the script's own state, so a fire missed to downtime is
# covered by the next one without a backfill; the window ends at the pinned
# fire time, never at a clock.

CHANGED_ORDERS = """
    SELECT order_id, region, amount, updated_at
      FROM sales.orders
     WHERE updated_at > from_iso8601_timestamp(:since)
       AND updated_at <= from_iso8601_timestamp(:until)
     ORDER BY updated_at
"""

def main():
    """Exports the orders changed since the last successful run."""
    since = run.state.get("synced_through", "1970-01-01T00:00:00Z")
    until = run.fire_time
    print("syncing orders changed in (%s, %s]" % (since, until))

    result = platform.query(
        connection = "primary",
        sql = CHANGED_ORDERS,
        params = {"since": since, "until": until},
    )

    rows = result["rows"]
    print("changed rows: %d" % len(rows))
    if rows:
        platform.export(name = "orders-delta-" + until, rows = rows, format = "csv")

    # Saved only if the run succeeds, and only if no other run of this script
    # wrote state in between. A run that fails above leaves the watermark alone.
    platform.save_state({"synced_through": until, "last_delta_rows": len(rows)})

def test_recorded_sync():
    """The recorded run exports what changed and moves the watermark to its fire time."""
    testing.replay("dpx_recording_of_a_draft")
    main()
    out = testing.outputs()
    first = out.exports[0].rows[0]
    assert.eq(first["order_id"], 1001)
    assert.eq(first["region"], "east")
    assert.eq(first["amount"], "120.00")
    assert.eq(first["updated_at"], "2026-09-20 08:15:00.000 UTC")
    assert.eq(out.state, {"synced_through": run.fire_time, "last_delta_rows": 3})
`,
	},
	{
		Name:        "example-nightly-maintenance",
		Description: "Scheduled maintenance of an application database: analyze it, then vacuum each table that churns, outside any transaction.",
		Source: `# Nightly maintenance of an application database, run on a schedule. It
# analyzes the whole database so the planner's statistics stay current, then
# vacuums each table that churns. A run acts as its author's persona, which
# must be the administrator or one the connection's schema_apply_personas
# names. Creating or dropping an index is a migration, never maintenance.

CONNECTION = "orders"

CHURNING = ["public.orders"]

def main():
    """Analyzes the database and vacuums each table that churns."""
    ran = [platform.maintain(connection = CONNECTION, command = "analyze").statement]
    for table in CHURNING:
        done = platform.maintain(connection = CONNECTION, command = "vacuum", table = table)
        ran.append(done.statement)
    print("ran: " + ", ".join(ran))
    platform.result({"statements": ran})

def test_maintains_a_copy():
    """On a throwaway copy of the database, analyzes it and vacuums each table that churns."""
    testing.database(CONNECTION)
    main()
    assert.eq(testing.outputs().result, {"statements": ["ANALYZE", "VACUUM (ANALYZE) \"public\".\"orders\""]})
`,
	},
	{
		Name:        "example-shop-checkout",
		Description: "A checkout form on an application database: an order, its lines and the stock they take written in one transaction, the customer read from the caller, and an oversell refused by the database.",
		Source: `# The checkout of a small shop (mcp:knowledge_page:platform-applications), bound
# as a form on an authenticated endpoint. Everything it writes is one
# transaction: the order, its lines and the stock they take commit together
# or not at all. The customer is the signed-in caller, read in SQL from
# mcp.email, and inventory.on_hand >= 0 is what refuses an oversell.

CONNECTION = "orders"

PLACE_ORDER = """
    INSERT INTO shop.orders (customer_email)
    VALUES (nullif(current_setting('mcp.email', true), ''))
    RETURNING id
"""

ADD_LINE = """
    INSERT INTO shop.order_lines (order_id, sku, qty, price)
    SELECT :order_id, sku, :qty, price FROM shop.products WHERE sku = :sku AND active
"""

TAKE_STOCK = "UPDATE shop.inventory SET on_hand = on_hand - :qty WHERE sku = :sku"

SET_TOTAL = """
    UPDATE shop.orders SET total = (SELECT sum(qty * price) FROM shop.order_lines WHERE order_id = :id)
     WHERE id = :id
"""

def lines_of(values):
    """Returns the submitted lines that name a product and a quantity."""
    out = []
    for n in ["1", "2"]:
        sku, qty = values.get("sku_" + n, ""), values.get("qty_" + n, "")
        if sku and qty:
            out.append({"sku": sku, "qty": int(qty)})
    return out

def place(lines):
    """Places one order and its lines and takes their stock, in one transaction."""

    def work(tx):
        """Writes the order, its lines and the stock, and returns the order id."""
        order_id = tx.query(PLACE_ORDER)["rows"][0]["id"]
        for line in lines:
            tx.execute(ADD_LINE, params = {"order_id": order_id, "sku": line["sku"], "qty": line["qty"]})
            tx.execute(TAKE_STOCK, params = line)
        tx.execute(SET_TOTAL, params = {"id": order_id})
        return order_id

    return platform.transaction(CONNECTION, work, on_error = "return")

def short_of_stock(got):
    """Reports whether a failed transaction was refused by the stock check."""
    text = str(got)
    return "check_violation" in text or "23514" in text

def form_spec(req):
    """The checkout form: up to two products and their quantities."""
    products = platform.query("SELECT sku, name FROM shop.products WHERE active ORDER BY name", connection = CONNECTION)
    choices = [{"value": p["sku"], "label": p["name"]} for p in products["rows"]]
    return {
        "fields": [
            {"name": "sku_1", "type": "single_choice", "required": True, "label": "Product", "choices": choices},
            {"name": "qty_1", "type": "number", "required": True, "label": "Quantity", "min": 1, "max": 100},
            {"name": "sku_2", "type": "single_choice", "label": "Second product", "choices": choices},
            {"name": "qty_2", "type": "number", "label": "Second quantity", "min": 1, "max": 100},
        ],
        "confirmation": "Thank you. Your order is placed.",
    }

def form_submit(req, values):
    """Places the order; refuses the form when the stock is short."""
    got = place(lines_of(values))
    if type(got) == "dict" and got.get("error"):
        if short_of_stock(got):
            return {"errors": {"qty_1": "There is not enough stock for this order."}}
        fail("the order was not placed: " + str(got["error"]))
    return {"confirmation": "Thank you. Order " + str(got) + " is placed."}

def main():
    """Drafts one submission of the form."""
    platform.result(form_submit({"method": "POST", "path": "/endpoints/checkout"}, {"sku_1": "mug", "qty_1": 1}))

def stock(sku):
    """Reads one product's stock."""
    return platform.query("SELECT on_hand FROM shop.inventory WHERE sku = :sku", connection = CONNECTION, params = {"sku": sku})["rows"][0]["on_hand"]

def given_a_mug_and_a_tee():
    """Sets two products and their stock in the test's copy of the database, whatever the migrations seeded."""
    testing.database(CONNECTION, schemas = ["shop"])
    platform.execute("""
        INSERT INTO shop.products (sku, name, price, active) VALUES ('mug', 'Mug', 12.50, true), ('tee', 'Tee', 20, true)
        ON CONFLICT (sku) DO UPDATE SET name = EXCLUDED.name, price = EXCLUDED.price, active = true
    """, connection = CONNECTION)
    platform.execute("""
        INSERT INTO shop.inventory (sku, on_hand) VALUES ('mug', 5), ('tee', 1)
        ON CONFLICT (sku) DO UPDATE SET on_hand = EXCLUDED.on_hand
    """, connection = CONNECTION)

def test_a_checkout_places_the_order_and_takes_the_stock():
    """Two lines become one order with its total, and each line's stock goes down."""
    given_a_mug_and_a_tee()
    got = form_submit({}, {"sku_1": "mug", "qty_1": 2, "sku_2": "tee", "qty_2": 1})
    assert.contains(got["confirmation"], "is placed")
    orders = platform.query("SELECT total FROM shop.orders", connection = CONNECTION)["rows"]
    assert.eq(len(orders), 1)
    assert.eq(str(orders[0]["total"]), "45.00")
    assert.eq(stock("mug"), 3)
    assert.eq(stock("tee"), 0)

def test_the_form_offers_each_active_product():
    """The form's product choices are the active products, by name."""
    given_a_mug_and_a_tee()
    spec = form_spec({})
    assert.eq([c["value"] for c in spec["fields"][0]["choices"]], ["mug", "tee"])

def test_the_draft_places_one_mug():
    """The draft's submission places an order for one mug."""
    given_a_mug_and_a_tee()
    main()
    assert.contains(testing.outputs().result["confirmation"], "is placed")
    assert.eq(stock("mug"), 4)

def test_an_oversell_leaves_no_order():
    """A line the stock cannot cover refuses the form and writes nothing."""
    given_a_mug_and_a_tee()
    got = form_submit({}, {"sku_1": "mug", "qty_1": 1, "sku_2": "tee", "qty_2": 2})
    assert.eq(got["errors"]["qty_1"], "There is not enough stock for this order.")
    assert.eq(len(platform.query("SELECT id FROM shop.orders", connection = CONNECTION)["rows"]), 0)
    assert.eq(stock("mug"), 5)
`,
	},
	{
		Name: ReferenceName,
		Description: "The reference script: helpers with docstrings, a bound connection parameter, a watermark in state, " +
			"a failure that says why, an export, a notification and a result, and the tests it is saved with.",
		Source: `# A weekly revenue report, written as every saved automation is: constants
# and functions at the top level, the work in main(), and the tests it is
# saved with at the end.

REVENUE_BY_REGION = """
    SELECT region, sum(amount) AS revenue, count(*) AS orders
      FROM sales.orders
     WHERE order_date > DATE :since AND order_date <= DATE :through
     GROUP BY region
     ORDER BY region
"""

# The channel the week's total is posted to.
CHANNEL = "revenue"

def window(state, fire_time):
    """The days since the last reported one, through the day before the fire."""
    through = date.add_days(date.of(fire_time), -1)
    since = state.get("reported_through", date.add_days(through, -7))
    return since, through

def summarize(rows):
    """The regions that had orders, and their revenue added up."""
    kept = [r for r in rows if r["orders"] > 0]

    # A DECIMAL column arrives as a string: convert before adding.
    return kept, sum([float(r["revenue"]) for r in kept])

def main():
    """Exports the week's revenue by region, posts the total and moves the watermark."""
    since, through = window(run.state, run.fire_time)
    result = platform.query(
        connection = run.params["connection"],
        sql = REVENUE_BY_REGION,
        params = {"since": since, "through": through},
    )
    kept, total = summarize(result["rows"])
    if not kept:
        fail("no orders between %s and %s" % (since, through))
    platform.export(name = "weekly-revenue", rows = kept, format = "csv")
    platform.notify(
        channel = CHANNEL,
        title = "Weekly revenue through " + through,
        body = "%d regions, %s in revenue" % (len(kept), total),
    )
    platform.save_state({"reported_through": through})
    platform.result({"regions": len(kept), "revenue": total})

def test_summarize_keeps_regions_with_orders():
    """A region with no orders is left out, and revenue adds up as numbers."""
    rows = [
        {"region": "east", "revenue": "10.50", "orders": 2},
        {"region": "west", "revenue": "0", "orders": 0},
    ]
    kept, total = summarize(rows)
    assert.eq([r["region"] for r in kept], ["east"])
    assert.eq(total, 10.5)

def test_window_starts_at_the_watermark():
    """A saved watermark starts the window; the fire time ends it."""
    since, through = window({"reported_through": "2026-09-13"}, "2026-09-21T07:00:00Z")
    assert.eq(since, "2026-09-13")
    assert.eq(through, "2026-09-20")

def test_a_recorded_week():
    """The recorded week exports each region, posts the total and moves the watermark."""

    # The id run_draft returned for a draft of this script. The draft stopped
    # at the post, which a draft does not make, so the test declares what
    # the notify tool answers it: nothing is posted, by the draft or the test.
    testing.replay("dpx_recording_of_a_draft")
    testing.answer("notify", {"action": "send", "channel": CHANNEL}, {
        "channel": CHANNEL,
        "kind": "chat",
        "queued": 1,
        "delivered": False,
        "detail": "accepted for delivery",
    })
    main()
    out = testing.outputs()
    assert.eq(out.exports[0].rows, [
        {"region": "east", "revenue": "10.50", "orders": 2},
        {"region": "west", "revenue": "4.25", "orders": 1},
    ])
    assert.eq(out.notifies[0]["body"], "2 regions, 14.75 in revenue")
    assert.eq(out.state, {"reported_through": "2026-09-20"})
    assert.eq(out.result, {"regions": 2, "revenue": 14.75})

def test_an_empty_week_fails():
    """A week with no orders fails naming the window and saves nothing."""
    testing.replay("dpx_recording_of_an_empty_week")
    assert.contains(assert.fails(main), "no orders between")
    assert.eq(testing.outputs().state, None)
`,
	},
	{
		Name: "policy-rate-limit",
		Description: "A policy (#2436): hold a call once its caller has made more than limit calls of one operation on one " +
			"connection in a rolling window; bind it with handler_set kind=policy.",
		Params: []script.Param{
			{Name: "limit", Type: script.ParamTypeInt, Description: "Calls allowed in the window", Required: true},
			{Name: "window", Type: script.ParamTypeString, Description: "How far back calls count, as 1h or 7d", Default: "1h"},
			{Name: "approvers", Type: script.ParamTypeList, Items: script.ParamTypeString, Description: "Who may approve a held call: addresses or personas", Default: []any{"administrators"}},
		},
		Source: `# A rate limit: a policy that holds a call once its caller has made more
# than limit calls of one operation on one connection in a rolling window.
# Bind it to the connection and operations it limits with manage_script
# handler_set kind=policy; limit, window and approvers are its args. Counts
# are this handler's own, kept by the platform for every replica, and a call
# that does not run counts against nobody.

def decide(call):
    """Allows the call while its caller's count in the window stays within the limit."""
    key = call["user"] + " " + call["connection"] + " " + call["operation"]
    used = platform.count(key, run.params["window"])
    limit = run.params["limit"]
    if used > limit:
        summary = "%d calls in %s; the limit is %d." % (used, run.params["window"], limit)
        return platform.require_approval(run.params["approvers"], summary)
    return platform.allow()

SAMPLE = {"user": "jo@example.com", "connection": "billing-api", "operation": "addSubscribers"}

def main():
    """Decides a sample call, so the script can be drafted."""
    print(decide(SAMPLE)["decision"])

def test_allows_within_the_limit():
    """A call within the limit runs."""
    testing.set_run(params = {"limit": 3, "window": "1h", "approvers": ["administrators"]})
    main()
    assert.eq(decide(SAMPLE)["decision"], "allow")

def test_holds_past_the_limit():
    """A call past the limit waits for the approvers the policy names."""
    testing.set_run(params = {"limit": 0, "window": "1h", "approvers": ["ops-leads"]})
    got = decide(SAMPLE)
    assert.eq(got["decision"], "require_approval")
    assert.eq(got["approvers"], ["ops-leads"])
    assert.eq(got["summary"], "1 calls in 1h; the limit is 0.")
`,
	},
	{
		Name: "policy-business-hours",
		Description: "A policy (#2436): hold a write outside business hours on a weekday, in UTC; reads always run. " +
			"Bind it with handler_set kind=policy.",
		Params: []script.Param{
			{Name: "start", Type: script.ParamTypeInt, Description: "The first hour writes run, 0-23 UTC", Default: 9},
			{Name: "end", Type: script.ParamTypeInt, Description: "The hour writes stop, 0-23 UTC", Default: 17},
		},
		Source: `# Business hours: a policy that lets a write run inside business hours on
# a weekday and holds it for an administrator outside them. A read always
# runs. Which calls count as writes is the handler's: POST, PUT, PATCH and
# DELETE unless its writes setting says otherwise. Hours are UTC.

WEEKEND = ["saturday", "sunday"]

def decide(call):
    """Allows a read, and a write inside business hours on a weekday."""
    if not call["write"]:
        return platform.allow()
    day = date.weekday(date.of(call["time"]))
    hour = int(call["time"][11:13])
    if day in WEEKEND or hour < run.params["start"] or hour >= run.params["end"]:
        summary = "a write on %s at %02d:00 UTC, outside %02d:00 to %02d:00 on a weekday." % (day, hour, run.params["start"], run.params["end"])
        return platform.require_approval([], summary)
    return platform.allow()

def main():
    """Decides a sample write, so the script can be drafted."""
    print(decide({"write": True, "time": "2026-11-13T15:00:00Z"})["decision"])

def test_a_write_inside_business_hours_runs():
    """A write on a Friday afternoon runs."""
    testing.set_run(params = {"start": 9, "end": 17})
    main()
    assert.eq(decide({"write": True, "time": "2026-11-13T15:00:00Z"})["decision"], "allow")

def test_a_write_outside_them_waits():
    """A write on a Saturday, or late on a weekday, waits; a read never does."""
    testing.set_run(params = {"start": 9, "end": 17})
    assert.eq(decide({"write": True, "time": "2026-11-14T10:00:00Z"})["decision"], "require_approval")
    assert.eq(decide({"write": True, "time": "2026-11-13T18:00:00Z"})["decision"], "require_approval")
    assert.eq(decide({"write": False, "time": "2026-11-14T10:00:00Z"})["decision"], "allow")
`,
	},
}

// Lookup returns the built-in example with the given name.
func Lookup(name string) (Example, bool) {
	for _, ex := range All {
		if ex.Name == name {
			return ex, true
		}
	}
	return Example{}, false
}
