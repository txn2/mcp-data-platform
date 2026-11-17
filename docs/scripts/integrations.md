# Integrations

Some platform operations need work only the deployment's environment knows how
to do: routing a new hostname through the ingress, creating a database,
registering a bucket. The platform cannot know how each organization does that,
and it does not try. An **integration** hands one such operation to a managed
script an administrator chooses. If an operation has a script, the platform
calls it; if it has none, the platform does nothing more than it does without
one (#2659).

What the script does is its own business: call an ingress API through an API
connection, call a provisioning service, or refuse. The platform asks for one
thing back, a status for each item it handed the script.

**Site routing** is the first operation.

## How an operation is run

- **One script per operation**, chosen in **Admin > Settings > Integrations** or
  with `PUT /api/v1/admin/integrations/{operation}`. The picker lists only scripts
  whose source declares the operation at its top level and defines its function
  (`INTEGRATION = "site_routing"` and `route(req)` for Site routing). A function
  named `route` in a script that declares no integration is one of its helpers:
  it is never offered, never called, and not checked for its parameters (#2683).
  The version that runs is the one approved for handlers, as for every handler
  kind; choosing a script can approve its latest version as you, and it then
  runs with your roles. Choosing, changing, clearing and retrying are audited.
- **The binding shows on both sides.** The operation's card names the script, and
  the script's page lists the operation among its handlers (kind
  `integration`). A save that removes the function from the chosen script is
  refused by the save gate, as it is for every handler kind. The script is not
  changed or cleared through `manage_script handler_*`; those commands point to
  the settings page.
- **Changes are computed, not described.** The platform computes the operation's
  items now, compares them with the items it last handed the script, and the
  difference is the change. Every write that can move the items (a site, an
  environment, a domain, an application, the pages domain) announces itself on
  one channel, and the replica holding the integrations' lock computes the
  difference. No call site describes its own change: an environment rename
  arrives as one hostname removed and one added.
- **And on a timer.** With nothing changed, the script is called every interval
  (15 minutes by default, 1 minute to 24 hours) with every item, so it can bring
  the environment back in line.
- **One run at a time per operation.** A change that arrives while a run is in
  flight is in the next run's difference. Two runs of one operation never overlap.
- **Asynchronous.** The operation that caused the change (adding a domain)
  returns at once. Its items read `pending` until the script answers.
- **A status per item.** The script returns `ready` or `failed` and a message for
  each item. The platform keeps the latest status of each and shows it wherever
  the item is shown. An item the result does not name keeps its status.
- **Failure.** If the script raises, times out or returns a malformed result,
  every item in that call is marked `failed`. The operation that caused the
  change is never undone. The next timer pass calls the script again, and
  **Retry** runs it at once.
- **Clearing the script** stops the calls and drops every status: the deployment
  is back to behaving as it does with no script.

An integration's run is a queued run of the chosen script (trigger
`integration`), executed by the run workers like any other run, with the
worker's limits and timeout. Only an administrator reads what it printed and
returned, and it is not recorded for replay. The request it was handed is not
shown on the run; a script that wants it on record prints it.

## Site routing

**Items:** every hostname the platform serves for sites and applications:

- each environment's address on the pages domain (`<site>.<pages domain>` for
  the environment holding the main address, `<site>--<env>.<pages domain>`
  otherwise);
- each application's page origin (`<app>.<pages domain>`);
- each domain added to a site environment or an application.

**The script declares `INTEGRATION = "site_routing"` and defines `route(req)`.**
The declaration is what makes `route` the operation's entry. Choosing a script and
saving the chosen one both require it, and the refusal names the line to add.
`req` carries:

| Field | Value |
|---|---|
| `reason` | `"change"` when hostnames were added or removed since the last run, `"timer"` otherwise |
| `added`, `removed` | The hostname entries added and removed since the last run started; `[]`, never null |
| `hosts` | Every hostname entry the platform serves now |

A hostname entry has `hostname`, `kind` (`pages`, `application` or `domain`),
`site` and `environment` or `application`, whichever applies, and `public`.

`route(req)` returns a dict keyed by hostname, each value
`{"status": "ready" | "failed", "message": "..."}`. A hostname the request did
not name is ignored. A removed hostname's status is dropped once the run that
carried the removal ends.

```python
INTEGRATION = "site_routing"

def route(req):
    """Hands the ingress every hostname the platform serves, in one call."""
    resp = platform.call("api_invoke_endpoint", {
        "connection": "ingress",
        "method": "PUT",
        "path": "/routes",
        "body": {"hosts": [{"hostname": h["hostname"], "public": h["public"]} for h in req["hosts"]]},
        "purpose": "Routing every hostname the platform serves.",
    })
    if resp["status"] >= 300:
        fail("the ingress answered %d" % resp["status"])
    failed = resp["body"].get("failed", {})
    return {
        h["hostname"]: {"status": "failed", "message": failed[h["hostname"]]} if h["hostname"] in failed else {"status": "ready", "message": "routed"}
        for h in req["hosts"]
    }
```

The call hands the ingress the whole set, so a hostname in `removed` is simply
absent from it; a script whose ingress takes one route at a time reads `added`
and `removed`. A request the ingress refuses fails the run, which marks every
hostname in the call failed; the next timer pass, or **Retry**, calls the script
again.

The `ingress` connection is yours: its credentials stay in the connection and
never enter `req` or the result.

**Where the status shows:**

- each address and domain row on a site's Domains tab, and an application's page
  address;
- `manage_site get` and `domains` (`routing` on each domain, `pages_routing` by
  environment) and the domain `add_domain` returns;
- the portal API: `GET /api/v1/portal/sites/{site}` (`routing`, `pages_routing`)
  and `GET /api/v1/portal/application-addresses/{name}` (`page_origin_routing`).

`routing` is `{status, message, checked_at}`. With no script chosen the field is
absent and the pages show nothing new.

## What each person sees

- The script's **message** is shown to site owners. It is untrusted text:
  control and invisible characters are removed, it is cut at 500 characters, and
  it is rendered as text, never HTML.
- When the **script itself failed**, a site owner reads "The site routing script
  failed" and when. The run's error, which can repeat what an upstream answered,
  is shown only to administrators: on the settings card, beside the failed row
  (`run_error`, `run_id`, `run_script_id` in the API), and on the run.
- **Retry** is offered on a failed row to anyone who can see the site
  (`POST /api/v1/portal/sites/{site}/routing/retry`), and on the settings card to
  administrators. Someone who is not an administrator is refused when nothing
  of the site failed (409) and within 15 seconds of the script's last run
  starting (429, saying when to ask again), so a reader can ask again for what
  failed but cannot keep the script running. Retrying while a run is in flight
  runs the script once more when that run ends.

## Admin API

| Route | What it does |
|---|---|
| `GET /api/v1/admin/integrations` | Every operation, its script and approved version, its last run (with the error), each item's status and the counts |
| `GET /api/v1/admin/integrations/{operation}/scripts` | The scripts whose latest version defines the operation's function |
| `PUT /api/v1/admin/integrations/{operation}` | Choose or change the script and the interval: `{"script_id", "interval_seconds", "approve"}`. Refused, naming the contract, when the version that would run does not define the function |
| `DELETE /api/v1/admin/integrations/{operation}` | Clear the script and every status |
| `POST /api/v1/admin/integrations/{operation}/retry` | Run the script now with every item |

## Upgrading to v1.163.0

A script is a Site routing script only when it declares `INTEGRATION =
"site_routing"` at its top level (#2683). In v1.162.2 any script with a top-level
`route` taking one parameter was offered in the picker, and a `route` helper with
any other number of parameters could not be saved. After the upgrade:

- a script that uses `route` as an internal helper saves again and leaves the
  picker; nothing else about it changes;
- a script already chosen for Site routing keeps running on its `route(req)`, but
  its next save is refused until the line `INTEGRATION = "site_routing"` is added
  at its top level. Add it when you next edit the script.
