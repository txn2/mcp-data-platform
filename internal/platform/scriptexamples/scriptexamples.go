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

// All is the seeded worked scripts. Three, not ten: they exist to show
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
