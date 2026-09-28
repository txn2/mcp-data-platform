// Package scriptexamples holds the built-in worked scripts manage_script
// offers an author (command=help lists them, get returns one by name). Every
// one is a whole script in the shape the authoring gates hold a new script to
// (#1913): its work in main(), each function documented, in the canonical
// format; a test in internal/platform/scriptlayer holds them to it.
package scriptexamples

// Example is one built-in worked script.
type Example struct {
	Name        string
	Description string
	Source      string
}

// ReferenceName is the complete reference script: every idiom a saved
// automation is built from, and the tests it is saved with (#1939). The
// built-in knowledge page platform-reference-script shows it verbatim.
const ReferenceName = "example-weekly-revenue"

// All is the seeded worked scripts. Four, not ten: they exist to show
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
    assert.eq([r["region"] for r in out.rows], ["east", "west"])
    assert.eq(out.rows[0]["total"], 1200)
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
    assert.eq(sum([r["total"] for r in out.rows]), 5400)
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
    assert.eq(out.exports[0].rows[0]["order_id"], 1001)
    assert.eq(out.state, {"synced_through": run.fire_time, "last_delta_rows": 3})
`,
	},
	{
		Name: ReferenceName,
		Description: "The reference script: helpers with docstrings, a bound connection parameter, a watermark in state, " +
			"a failure that says why, an export and a result, and the tests it is saved with.",
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
    """Exports the week's revenue by region and moves the watermark."""
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
    """The recorded week exports each region and moves the watermark."""

    # The id run_draft returned for a draft of this script.
    testing.replay("dpx_recording_of_a_draft")
    main()
    out = testing.outputs()
    assert.eq([r["region"] for r in out.exports[0].rows], ["east", "west"])
    assert.eq(out.state, {"reported_through": "2026-09-20"})
    assert.eq(out.result["regions"], 2)

def test_an_empty_week_fails():
    """A week with no orders fails naming the window and saves nothing."""
    testing.replay("dpx_recording_of_an_empty_week")
    assert.contains(assert.fails(main), "no orders between")
    assert.eq(testing.outputs().state, None)
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
