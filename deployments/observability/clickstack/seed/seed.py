#!/usr/bin/env python3
"""Apply the mcp-data-platform dashboards, saved searches and alerts to HyperDX.

Reads the definitions beside this file (dashboards/*.json, saved-searches.json,
alerts.json) and applies them through HyperDX's API (/api/v2), idempotently by
name: a definition that does not exist is created, one that differs from the
definition is replaced, and one that matches is left alone. What an earlier
seed created (tagged mcp-data-platform) and the definitions no longer name is
removed; nothing an operator made without the tag is touched. A second run
over an unchanged directory writes nothing.

Credentials, one of:

  HYPERDX_API_KEY        a personal API access key (Team Settings > API Keys).
                         The Kubernetes Job reads it from a Secret.
  --bootstrap            the compose profile's evaluation path: create the
                         first user (HYPERDX_SEED_EMAIL, HYPERDX_SEED_PASSWORD)
                         on a fresh install, or sign in as it, and read its key.
                         It uses the same sign-up and sign-in routes HyperDX's
                         own evaluation harness does; they are not part of the
                         documented API.

HYPERDX_API_URL is the API (default http://localhost:8000).
HYPERDX_ALERT_WEBHOOK_URL, when set, is the generic webhook every alert is sent
to; with none set the alerts are skipped, since an alert needs a channel.
HYPERDX_ALERT_INTERVAL (1m, 5m, ...) evaluates every alert on that interval
instead of its own, for an evaluation that should not wait out production
windows; run the seed again without it to restore them.

Definitions are written for the platform's OTel tables in ClickHouse. A tile's
or alert's "sql" may use {{fleet}}, which becomes the dashboard's fleet filter
(deployment, environment, version), and HyperDX's own $__ macros.

Standard library only: it runs in the stock python image.
"""

import argparse
import json
import os
import re
import sys
import time
import urllib.error
import urllib.request

# The dashboard variables every dashboard carries, and the clause {{fleet}}
# becomes. Each reads a resource attribute the platform stamps on every
# signal; HyperDX's $__filter expands to 1=1 when nothing is selected, so a
# single deployment leaves them empty.
FLEET_FILTERS = [
    ("Deployment", "deployment", "ResourceAttributes['mcp_platform.deployment.id']"),
    ("Environment", "environment", "ResourceAttributes['deployment.environment.name']"),
    ("Version", "version", "ResourceAttributes['service.version']"),
]
FLEET_CLAUSE = " AND ".join(f"$__filter({expr}, ${var})" for _, var, expr in FLEET_FILTERS)

# Tags the seed puts on what it owns, so an operator can tell them apart.
SEED_TAG = "mcp-data-platform"


class APIError(Exception):
    """A refused or failed API call, with what the server said."""


class Client:
    """The few HyperDX API calls the seed makes."""

    def __init__(self, base, key=None, timeout=30):
        self.base = base.rstrip("/")
        self.key = key
        self.timeout = timeout
        self.writes = 0

    def call(self, method, path, body=None, headers=None):
        data = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request(self.base + path, data=data, method=method)
        req.add_header("Accept", "application/json")
        if data is not None:
            req.add_header("Content-Type", "application/json")
        if self.key:
            req.add_header("Authorization", "Bearer " + self.key)
        for k, v in (headers or {}).items():
            req.add_header(k, v)
        for attempt in range(RATE_LIMIT_RETRIES + 1):
            try:
                with urllib.request.urlopen(req, timeout=self.timeout) as resp:  # noqa: S310 -- the operator's own API URL
                    raw = resp.read()
                    if method in ("POST", "PUT", "DELETE") and path.startswith("/api/v2/"):
                        self.writes += 1
                    return json.loads(raw) if raw else None, resp.headers
            except urllib.error.HTTPError as e:
                if e.code == 429 and attempt < RATE_LIMIT_RETRIES:
                    # HyperDX limits each key to EXTERNAL_API_RATE_LIMIT_MAX
                    # requests a minute (100 by default); wait for the window.
                    time.sleep(retry_after(e.headers))
                    continue
                detail = e.read().decode(errors="replace")[:500]
                raise APIError(f"{method} {path}: {e.code} {detail}") from e
        raise APIError(f"{method} {path}: still rate limited")

    def list(self, kind):
        body, _ = self.call("GET", f"/api/v2/{kind}")
        return body.get("data", body) if isinstance(body, dict) else body


# How often a rate-limited call is retried, and the longest wait between tries.
RATE_LIMIT_RETRIES = 5
RATE_LIMIT_MAX_WAIT = 65


def retry_after(headers):
    """Seconds until a rate-limited call may be retried: Retry-After, else the
    standard RateLimit-Reset, else ten, never more than a window and a bit."""
    for name in ("Retry-After", "RateLimit-Reset"):
        value = (headers or {}).get(name)
        if value is not None:
            try:
                return min(max(float(value), 0.0), RATE_LIMIT_MAX_WAIT)
            except ValueError:
                pass
    return 10.0


class NoRedirect(urllib.request.HTTPRedirectHandler):
    """Sign-in answers with a redirect whose cookie is what the seed wants."""

    def redirect_request(self, *args, **kwargs):  # noqa: D102
        return None


def bootstrap_key(base, email, password, timeout=30):
    """Create the first user (or find it exists), sign in, and read its key.

    HyperDX scopes its session cookie to Domain=localhost, so a cookie jar
    would not send it to any other host; the cookie is carried by hand.
    """
    opener = urllib.request.build_opener(NoRedirect)

    def post(path, body):
        req = urllib.request.Request(
            base.rstrip("/") + path, data=json.dumps(body).encode(), method="POST",
            headers={"Content-Type": "application/json"})
        try:
            resp = opener.open(req, timeout=timeout)
            return resp.status, resp.headers
        except urllib.error.HTTPError as e:
            return e.code, e.headers

    # 200 when the user is created; an existing user answers 400 and the
    # sign-in below decides whether the password is right.
    post("/register/password", {"email": email, "password": password, "confirmPassword": password})
    status, headers = post("/login/password", {"email": email, "password": password})
    location = headers.get("Location", "") if headers else ""
    cookie = ""
    for value in (headers.get_all("Set-Cookie") or []) if headers else []:
        if value.startswith("connect.sid="):
            cookie = value.split(";", 1)[0]
    if status >= 400 or "err=" in location or not cookie:
        raise APIError(f"sign-in as {email} failed ({status}{', ' + location if location else ''})")
    me, _ = Client(base, timeout=timeout).call("GET", "/me", headers={"Cookie": cookie})
    key = (me or {}).get("accessKey")
    if not key:
        raise APIError("the account has no access key")
    return key


def subset(want, have):
    """Whether every key in want is in have with the same value (recursively).

    The server adds ids, timestamps and defaults to what it stores; those are
    not differences. A list must match element for element.
    """
    if isinstance(want, dict):
        return isinstance(have, dict) and all(k in have and subset(v, have[k]) for k, v in want.items())
    if isinstance(want, list):
        return isinstance(have, list) and len(want) == len(have) and all(subset(a, b) for a, b in zip(want, have))
    return want == have


def by_name(items):
    return {i.get("name"): i for i in items or []}


def apply(client, kind, desired, existing, report, fetch_one=False):
    """Create, replace or leave one named object."""
    name = desired["name"]
    current = existing.get(name)
    if current is None:
        client.call("POST", f"/api/v2/{kind}", desired)
        report.append(f"created {kind}: {name}")
        return
    if fetch_one:
        # A list response leaves fields out (an alert's chartConfig); compare
        # with the full object.
        full, _ = client.call("GET", f"/api/v2/{kind}/{current['id']}")
        current = full.get("data", full) if isinstance(full, dict) else full
    if subset(desired, current):
        report.append(f"unchanged {kind}: {name}")
        return
    client.call("PUT", f"/api/v2/{kind}/{current['id']}", for_update(kind, desired, current))
    report.append(f"updated {kind}: {name}")


def prune(client, kind, desired_names, existing, report):
    """Remove what an earlier seed created and the definitions no longer
    name: an object tagged SEED_TAG whose name is not defined. Anything an
    operator made without the tag is left alone."""
    for name, obj in existing.items():
        if name not in desired_names and SEED_TAG in (obj.get("tags") or []):
            client.call("DELETE", f"/api/v2/{kind}/{obj['id']}")
            report.append(f"removed {kind}: {name}")


def for_update(kind, desired, current):
    """The body a replacement is sent with. A dashboard's update names each
    filter by the id HyperDX gave it (its create takes none): an existing
    filter keeps its id, matched by name, and a new one is given its variable
    name."""
    if kind != "dashboards":
        return desired
    ids = {f.get("name"): f.get("id") for f in current.get("filters") or [] if f.get("id")}
    body = dict(desired)
    body["filters"] = [{**f, "id": ids.get(f["name"]) or f["variableName"]} for f in desired.get("filters", [])]
    return body


def sources(client):
    """The connection and the log, trace and metric sources by kind."""
    out = {}
    for s in client.list("sources"):
        out.setdefault(s["kind"], s)
    missing = {"log", "trace", "metric"} - out.keys()
    if missing:
        raise APIError("HyperDX has no " + ", ".join(sorted(missing)) + " source; is this a ClickStack install?")
    return out


def expand_sql(sql):
    return sql.replace("{{fleet}}", FLEET_CLAUSE)


def alert_sql(spec):
    """An alert's SQL. An alert has no dashboard and so no variables: the
    fleet clause is left out (HyperDX refuses a $__filter whose variable it
    cannot resolve), and an alert reads the whole fleet, grouping by
    deployment where it compares deployments."""
    return render_query(spec["query"], "number").replace("{{fleet}}", "1=1")


# The OTel metric tables ClickStack writes, by the kind of instrument.
TABLES = {"sum": "otel_metrics_sum", "gauge": "otel_metrics_gauge", "histogram": "otel_metrics_histogram"}

# The bucket every series is read at: the chart's own granularity on a time
# axis, a minute everywhere else (a number or a table has no axis).
LINE_BUCKET = "$__timeInterval(TimeUnix)"
FLAT_BUCKET = "toStartOfMinute(TimeUnix)"


def _window(q):
    """The time condition: the chart's range, or for an alert a window
    reaching back before the evaluation window ("lookback") or lying wholly
    before it ("baseline"), as an SQL interval such as "1 DAY"."""
    if q.get("lookback"):
        return f"TimeUnix >= $__fromTime - INTERVAL {q['lookback']} AND TimeUnix <= $__toTime"
    if q.get("baseline"):
        return f"TimeUnix >= $__fromTime - INTERVAL {q['baseline']} AND TimeUnix < $__fromTime"
    return "$__timeFilter(TimeUnix)"


def _where(q):
    clauses = [f"MetricName = '{q['metric']}'", _window(q), "{{fleet}}"]
    if q.get("where"):
        clauses.append(f"({q['where']})")
    return " AND ".join(clauses)


# The deployment a series belongs to: what a gauge every replica reports alike
# is deduplicated within, before deployments are combined.
DEPLOYMENT = "ResourceAttributes['mcp_platform.deployment.id']"


def _latest(q, table, value, bucket, deployment=False):
    """One row per series and bucket: the series' last value in the bucket,
    with the series' deployment as d when asked."""
    by = f", {q['by']} AS g" if q.get("by") else ""
    dep = f", {DEPLOYMENT} AS d" if deployment else ""
    return (f"SELECT {bucket} AS ts, cityHash64(Attributes, ResourceAttributes) AS sid{by}{dep}, "
            f"argMax({value}, TimeUnix) AS v FROM {TABLES[table]} WHERE {_where(q)} "
            f"GROUP BY ts, sid{', g' if by else ''}{', d' if deployment else ''}")


def _increase(q, bucket):
    """Per series and bucket, how much a cumulative counter rose since the
    series' previous bucket. Read across buckets, not within one: at the
    platform's 60s export interval a bucket usually holds one sample. A drop
    is a process restart, counted from zero."""
    g = ", g" if q.get("by") else ""
    return (f"SELECT ts{g}, if(v >= p, v - p, v) AS inc FROM (SELECT ts, sid{g}, v, "
            f"lagInFrame(v, 1, v) OVER (PARTITION BY sid ORDER BY ts ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) AS p "
            f"FROM ({_latest(q, 'sum', 'Value', bucket)}))")


def render_query(q, display):
    """The SQL for one tile or alert query spec, with a LIMIT when it names one.

    A table's or number's rows are ordered by value, largest first.
    """
    sql = _render_query(q, display)
    return f"{sql} LIMIT {int(q['limit'])}" if q.get("limit") else sql


def _render_query(q, display):
    """The SQL for one query spec, by kind.

    kind rate      a counter per second (line) or its total over the range
    kind ratio     the share of a counter matching q['match'], as a percent
    kind quantile  a histogram quantile q['q'], in the metric's own unit
    kind gauge     a gauge's last value per series, combined with q['agg'];
                   q['table'] = "sum" reads an up-down counter, which OTel
                   stores as a sum but which holds a level; q['shared'] marks
                   a gauge every replica reports alike (the database's and
                   the buckets' figures, queue depths, connection states),
                   read with max within each deployment and then combined
                   across deployments with q['agg'], so three replicas never
                   read as three times the figure
    kind regression  the largest ratio, over q['by'], of a histogram quantile
                   in the window to the same quantile over q['baseline'] before it
    kind compose   q['expr'] with each {name} replaced by q['parts'][name],
                   built as a number (no time axis)
    kind sql       q['sql'] as written
    """
    kind = q["kind"]
    if kind == "sql":
        return q["sql"]
    if kind == "compose":
        sql = q["expr"]
        for name, part in q["parts"].items():
            sql = sql.replace("{" + name + "}", render_query(part, "number"))
        return sql
    if kind == "regression":
        part = {k: v for k, v in q.items() if k not in ("kind", "baseline", "label", "by_name")}
        part.update(kind="quantile", by_name="g", label="v")
        now = _render_query(part, "table")
        then = _render_query({**part, "baseline": q["baseline"]}, "table")
        return (f"SELECT round(max(w.v / b.v), 2) AS {q.get('label', 'ratio')} FROM ({now}) AS w "
                f"INNER JOIN ({then}) AS b USING (g) WHERE b.v > 0")
    line = display in ("line", "stacked_bar")
    bucket = LINE_BUCKET if line else FLAT_BUCKET
    by = q.get("by")
    label = q.get("label", "value")
    head, group, order = ("ts, ", "GROUP BY ts", " ORDER BY ts") if line else ("", "", "")
    if by:
        head += f"g AS {q.get('by_name', 'group')}, "
        group = (group + ", g") if group else "GROUP BY g"
        if not line:
            order = f" ORDER BY {label} DESC"
    if kind == "rate":
        value = "sum(inc) / $__interval_s" if line else "sum(inc)"
        return f"SELECT {head}round({value}, 4) AS {label} FROM ({_increase(q, bucket)}) {group}{order}"
    if kind == "ratio":
        # The match flag rides beside the group (or as it, with none) so one
        # pass sums both the matching increase and the whole.
        if by:
            # The pair is renamed m: the group's own alias may be g.
            pairs = _increase({**q, "by": f"tuple({by}, {q['match']})"}, bucket)
            inner = f"SELECT {'ts, ' if line else ''}g AS m, inc FROM ({pairs})"
            head = head.replace("g AS", "m.1 AS")
            group = group.replace(", g", ", m.1").replace("BY g", "BY m.1")
            share = "sumIf(inc, m.2) / sum(inc)"
        else:
            inner = _increase({**q, "by": f"({q['match']})"}, bucket)
            share = "sumIf(inc, g) / sum(inc)"
        return f"SELECT {head}round(100 * {share}, 2) AS {label} FROM ({inner}) {group} HAVING sum(inc) > 0{order}"
    if kind == "quantile":
        g = ", g" if by else ""
        latest = _latest(q, "histogram", "BucketCounts", bucket).replace(
            "argMax(BucketCounts, TimeUnix) AS v", "any(ExplicitBounds) AS bounds, argMax(BucketCounts, TimeUnix) AS v")
        lagged = (f"SELECT ts, sid{g}, bounds, v, lagInFrame(v, 1, v) OVER (PARTITION BY sid ORDER BY ts "
                  f"ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) AS p FROM ({latest})")
        inc = (f"SELECT ts{g}, bounds, if(arraySum(v) >= arraySum(p), arrayMap((x, y) -> toInt64(x) - toInt64(y), v, p), "
               f"arrayMap(x -> toInt64(x), v)) AS inc FROM ({lagged})")
        summed = f"SELECT {'ts, ' if line else ''}{'g, ' if by else ''}any(bounds) AS b, sumForEach(inc) AS n FROM ({inc}) {group}"
        pick = f"b[least(arrayFirstIndex(c -> c >= {q['q']} * arraySum(n), arrayCumSum(n)), length(b))]"
        return f"SELECT {head}round({pick}, 4) AS {label} FROM ({summed}) WHERE arraySum(n) > 0{order}"
    if kind == "gauge":
        agg = q.get("agg", "sum")
        g = ", g" if by else ""
        series = _latest(q, q.get("table", "gauge"), "Value", bucket, deployment=bool(q.get("shared")))
        if q.get("shared"):
            series = f"SELECT ts{g}, d, max(v) AS v FROM ({series}) GROUP BY ts{g}, d"
        per_bucket = f"SELECT ts{g}, {agg}(v) AS v FROM ({series}) GROUP BY ts{g}"
        if line:
            return f"SELECT {head}round(max(v), 4) AS {label} FROM ({per_bucket}) {group}{order}"
        last = f"SELECT {'g, ' if by else ''}argMax(v, ts) AS v FROM ({per_bucket}){' GROUP BY g' if by else ''}"
        return f"SELECT {head}round(v, 4) AS {label} FROM ({last}){order}"
    raise ValueError(f"unknown query kind {kind!r}")


def tile_sql(tile):
    return expand_sql(render_query(tile["query"], tile["display"]))


def tile_config(tile, connection_id, source_id):
    cfg = {"displayType": tile["display"], "configType": "sql", "connectionId": connection_id,
           "sourceId": source_id, "sqlTemplate": tile_sql(tile)}
    if "format" in tile:
        cfg["numberFormat"] = tile["format"]
    return cfg


def dashboard_body(spec, src):
    connection = src["metric"]["connection"]
    metrics = src["metric"]["id"]
    filters = [{"type": "QUERY_EXPRESSION", "name": label, "expression": expr, "sourceId": metrics,
                "sourceMetricType": "gauge", "isVariableEnabled": True, "variableName": var}
               for label, var, expr in FLEET_FILTERS]
    tiles = []
    for t in spec["tiles"]:
        source = src[t.get("source", "metric")]["id"]
        tiles.append({"name": t["name"], "x": t["x"], "y": t["y"], "w": t["w"], "h": t["h"],
                      "config": tile_config(t, connection, source)})
    return {"name": spec["name"], "tags": [SEED_TAG] + spec.get("tags", []), "filters": filters, "tiles": tiles}


def saved_search_body(spec, src):
    body = {"name": spec["name"], "sourceId": src[spec["source"]]["id"], "where": spec["where"],
            "whereLanguage": spec.get("whereLanguage", "sql"), "tags": [SEED_TAG]}
    for k in ("select", "orderBy"):
        if k in spec:
            body[k] = spec[k]
    return body


def alert_body(spec, src, webhook_id, interval=None):
    connection = src["metric"]["connection"]
    body = {
        "name": spec["name"], "source": "inline", "interval": interval or spec["interval"],
        "threshold": spec["threshold"], "thresholdType": spec["thresholdType"],
        "tags": [SEED_TAG], "channel": {"type": "webhook", "webhookId": webhook_id},
        "chartConfig": {"displayType": "number", "configType": "sql", "connectionId": connection,
                        "sqlTemplate": alert_sql(spec), "name": spec["name"]},
    }
    if spec.get("windows"):
        body["numConsecutiveWindows"] = spec["windows"]
    if spec.get("message"):
        body["message"] = spec["message"]
    return body


def expand_macros(sql, minutes=60, interval_s=60, selected=None):
    """HyperDX's macros as the dashboard would expand them over the last
    `minutes`: what --render prints, so a definition can be run against
    ClickHouse without HyperDX. selected maps a dashboard variable to the value
    picked in its filter; a variable with none is 1=1, as in HyperDX."""
    selected = selected or {}

    def pick(m):
        expr, var = m.group(1).rsplit(",", 1)
        value = selected.get(var.strip().lstrip("$"))
        if value is None:
            return "1=1"
        return f"{expr.strip()} IN ('{value.replace(chr(39), chr(39) * 2)}')"
    sql = re.sub(r"\$__filter\(([^()]*)\)", pick, sql)
    sql = sql.replace("$__fromTime", f"(now() - INTERVAL {minutes} MINUTE)").replace("$__toTime", "now()")
    sql = re.sub(r"\$__timeFilter\(([^()]*)\)",
                 lambda m: f"{m.group(1)} >= now() - INTERVAL {minutes} MINUTE AND {m.group(1)} <= now()", sql)
    sql = re.sub(r"\$__timeInterval\(([^()]*)\)",
                 lambda m: f"toStartOfInterval(toDateTime({m.group(1)}), INTERVAL {interval_s} second)", sql)
    return sql.replace("$__interval_s", str(interval_s))


def rendered(directory, selected=None):
    """Every query the definitions hold, macros expanded: (what, name, sql)."""
    dashboards, _, alerts = load(directory)
    for d in dashboards:
        for t in d["tiles"]:
            yield "tile", f"{d['name']} / {t['name']}", expand_macros(tile_sql(t), selected=selected)
    for a in alerts:
        yield "alert", a["name"], expand_macros(alert_sql(a), selected=selected)


def load(directory):
    dashboards = []
    ddir = os.path.join(directory, "dashboards")
    for name in sorted(os.listdir(ddir)):
        if name.endswith(".json"):
            with open(os.path.join(ddir, name)) as f:
                dashboards.append(json.load(f))
    with open(os.path.join(directory, "saved-searches.json")) as f:
        searches = json.load(f)
    with open(os.path.join(directory, "alerts.json")) as f:
        alerts = json.load(f)
    return dashboards, searches, alerts


def wait_ready(base, deadline):
    while True:
        try:
            Client(base, timeout=5).call("GET", "/health")
            return
        except (APIError, OSError):
            if time.monotonic() > deadline:
                raise
            time.sleep(2)


def run(argv=None):
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--dir", default=os.path.dirname(os.path.abspath(__file__)))
    p.add_argument("--bootstrap", action="store_true")
    p.add_argument("--wait", type=float, default=300, help="seconds to wait for the API")
    p.add_argument("--render", action="store_true",
                   help="print every query as JSON lines, macros expanded, and change nothing")
    p.add_argument("--filter", action="append", default=[], metavar="VAR=VALUE",
                   help="with --render, a dashboard filter's selected value (deployment, environment, version)")
    args = p.parse_args(argv)
    if args.render:
        selected = dict(f.split("=", 1) for f in args.filter)
        for what, name, sql in rendered(args.dir, selected):
            print(json.dumps({"what": what, "name": name, "sql": sql}))
        return 0

    base = os.environ.get("HYPERDX_API_URL", "http://localhost:8000")
    wait_ready(base, time.monotonic() + args.wait)
    key = os.environ.get("HYPERDX_API_KEY", "")
    if not key and args.bootstrap:
        key = bootstrap_key(base, os.environ["HYPERDX_SEED_EMAIL"], os.environ["HYPERDX_SEED_PASSWORD"])
    if not key:
        raise APIError("set HYPERDX_API_KEY, or pass --bootstrap with HYPERDX_SEED_EMAIL and HYPERDX_SEED_PASSWORD")

    client = Client(base, key)
    dashboards, searches, alerts = load(args.dir)
    src = sources(client)
    report = []

    existing = by_name(client.list("dashboards"))
    for spec in dashboards:
        apply(client, "dashboards", dashboard_body(spec, src), existing, report)
    prune(client, "dashboards", {d["name"] for d in dashboards}, existing, report)
    existing = by_name(client.list("saved-searches"))
    for spec in searches:
        apply(client, "saved-searches", saved_search_body(spec, src), existing, report)
    prune(client, "saved-searches", {s["name"] for s in searches}, existing, report)

    url = os.environ.get("HYPERDX_ALERT_WEBHOOK_URL", "")
    if url:
        hook = {"name": "mcp-data-platform alerts", "service": "generic", "url": url,
                "description": "Alerts the mcp-data-platform seed created"}
        apply(client, "webhooks", hook, by_name(client.list("webhooks")), report)
        webhook_id = by_name(client.list("webhooks"))[hook["name"]]["id"]
        existing = by_name(client.list("alerts"))
        for spec in alerts:
            apply(client, "alerts", alert_body(spec, src, webhook_id, os.environ.get("HYPERDX_ALERT_INTERVAL")),
                  existing, report, fetch_one=True)
        prune(client, "alerts", {a["name"] for a in alerts}, existing, report)
    else:
        report.append(f"skipped {len(alerts)} alerts: HYPERDX_ALERT_WEBHOOK_URL is not set")

    for line in report:
        print(line)
    print(f"seed: {client.writes} writes")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(run())
    except (APIError, OSError, KeyError, ValueError) as e:
        print(f"seed: {e}", file=sys.stderr)
        sys.exit(1)
