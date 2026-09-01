---
description: "Managed scripts in the portal: the list, one script's page, its schedule, source, versions, runs, and state."
---

# Scripts

A script is a program the platform runs for you: an agent writes one once, and from then
on it produces the same report, dashboard refresh, or export on a schedule or on request.
A script runs as soon as it is saved, under the access its author holds. The Scripts page
is where you see what you have, what is scheduled, and how it has been going, over two
tabs: **Scripts** and **Runs**.

![Scripts](../images/screenshots/light/user-scripts-light.webp#only-light)![Scripts](../images/screenshots/dark/user-scripts-dark.webp#only-dark)

Above the table are three numbers, and each of them is also the control that shows what
it counted: **Scripts**, **Scheduled** (anything with a schedule, paused or not), and
**Failing** — the scripts whose last run failed, which is the number most people open
this page for. Pressing a tile narrows the table to the scripts it counted; pressing it
again, or pressing **Scripts**, shows all of them.

Every script here is yours: a script is one person's, so this page needs no owner column
and shows nobody else's. An administrator can move a script to another owner, which is
how one arrives here that you did not write.

Each row states what is worth knowing at a glance: what the script is called, its
schedule and next fire, and how its most recent run ended. A script that will execute
nothing carries a badge beside its name — **disabled**, or its lifecycle status — because
that is the exception you scan a list for; the version a run executes is true of every
healthy script and is stated on the script's own page. Opening a row opens the script, the
way every other list in the portal opens a record. A script with no schedule runs on
demand; a paused schedule says so rather than showing a next fire that will not happen.

Each row also shows how the script is filed: the category it belongs to and the tags it
carries. Under the tiles are a search box and a chip per category, with the tags on a
second row. The search matches what a script is called and what it says about itself, and
pressing an active chip again clears it. All three are applied by the server, so they
cover every script you can see rather than only the ones already on screen.

The schedule is stated in words, always — "Every weekday at 7:00 AM,
America/Los_Angeles", "Every 30 minutes, UTC" — because this is the column you scan to
answer what is running and when, and a cron expression is not an answer to that. A
schedule with no phrase for it is named as a custom schedule; the expression itself is in
the schedule editor on the script's own page, which is where one is read and written.

Before an agent has written anything for you, the page says so rather than showing an
empty table.

![No scripts yet](../images/screenshots/light/user-scripts-empty-light.webp#only-light)![No scripts yet](../images/screenshots/dark/user-scripts-empty-dark.webp#only-dark)

## Every run, across your scripts

The **Runs** tab answers the question the run history on one script cannot: not how is
this report going, but how are your scripts going, all of them. Every run of every script
you own, newest first, with what triggered it, how it ended, how long it took, and — when
it failed — the reason, in the row rather than behind it.

![Runs across your scripts](../images/screenshots/light/user-scripts-runs-light.webp#only-light)![Runs across your scripts](../images/screenshots/dark/user-scripts-runs-dark.webp#only-dark)

Opening a row opens that run: the script's page, with the run's log, its parameters and
what it produced already open. The script's name in the row opens the script itself. A
listing that fills its cap says so, and each script's own page carries its full history.

## One script

Opening a script shows its **Details**: who owns it, which version runs, the schedule it
fires on, when it fires next, and the parameters a run binds against. These are the same
facts an agent gets when it resolves a reference to the script, so the page and your
agent describe the script identically.

The page is ordered the way a script is debugged. Details first, then the schedule, then
what the script says about itself, then the code — and the run history directly under the
code, so an error in the history is answered by the text above it.

The schedule is folded, and says what the script runs without being opened: "Runs: Every
weekday at 7:00 AM, America/Los_Angeles", or "Not scheduled". Open it to change the
cadence; pausing and resuming are on the header either way. **About** starts open, because
what a script is for is what you came to read, and folds away when the document is long
enough to be in the way.

![Script detail](../images/screenshots/light/user-script-detail-light.webp#only-light)![Script detail](../images/screenshots/dark/user-script-detail-dark.webp#only-dark)

When a run would be refused — the script disabled or retired — the page carries the
platform's own reason for the refusal rather than leaving you to work it out from the
status.

## The schedule

Below the details, on a script you own, is when it runs — folded, with what it does now in
the header. Open it and pick how often — hourly, daily,
weekdays, chosen days of the week, or a day of the month — set the time and the timezone
it is read in, bind the value every fire passes, and pause or resume the whole thing.

You do not have to know cron. The page states what it will save in words ("Every weekday
at 7:00 AM, America/Los_Angeles") and shows the expression it produces underneath, and
there is a **Custom** choice for a schedule the builder cannot express. A schedule an
agent wrote through `manage_script` that the builder cannot express opens there, as
itself, rather than being rewritten into something near it.

The time is read in the zone beside it, so a report keeps its wall clock across a
daylight-saving change, and the floor is one fire a minute. A monthly schedule past the
28th says plainly that the months without that day are skipped rather than moved.

A date parameter usually wants `${fire_date}`, which expands to the day the schedule
fires rather than to the day you typed it — that is what makes a scheduled run
reproducible, because the run records the date it was computing for.

Pausing is its own control rather than a schedule you have to clear and retype. A paused
schedule resumes on the fire it was parked on, and there is no way to delete one: the
schedule is part of the explanation of the runs it produced. Fires that came due while
the platform was not running them are counted and stated rather than caught up on, so a
gap in a script's schedule is visible instead of turning into a burst of stale reports.

![Paused schedule](../images/screenshots/light/user-script-schedule-paused-light.webp#only-light)![Paused schedule](../images/screenshots/dark/user-script-schedule-paused-dark.webp#only-dark)

A schedule on a disabled or retired script saves, and fires nothing until the script is
back in service, which the page says plainly rather than leaving you waiting on a run
that was never going to happen.

## About

What the script says about itself, written as a document rather than a caption:
markdown, rendered the way an asset's description and a knowledge page are, with the
category and tags it is filed under above it. On a script you own, **Edit** opens the
four fields together — display name, category, tags, and the description, with a live
preview beside what you type.

![Documenting a script](../images/screenshots/light/user-script-documentation-light.webp#only-light)![Documenting a script](../images/screenshots/dark/user-script-documentation-dark.webp#only-dark)

None of the four changes what the script does, so saving them applies at once: nothing is
sent for review, and the version that is running is untouched. Write what the script
produces, what each parameter means, and what it assumes about the data — this is what
somebody reading the script in six months has instead of the code, and it is part of what
search matches the script on. A description long enough to be a document in its own right
is still saved, with a suggestion that the background might belong in a knowledge page you
link to.

## The code, and running it

On a script you own, the source is editable in place, with Starlark highlighted as the
Python dialect it is. Saving makes the edit the version that runs: `run_script` executes
it, any schedule fires it, and it runs under the access you hold when you save.

![Script source](../images/screenshots/light/user-script-source-light.webp#only-light)![Script source](../images/screenshots/dark/user-script-source-dark.webp#only-dark)

Source that does not parse is refused when you save it, naming what to fix, rather than
failing at the next run with nobody watching.

**Run** and **Dry run** sit side by side above the editor, because they are the same
question asked of two texts: Run executes the saved version, a dry run executes what is
on screen. One parameter form below the editor supplies the values for both.

Run produces fresh output without waiting for the next scheduled fire, and queues exactly
what an agent's `run_script` queues: the platform executes it the same way a scheduled
fire is executed, and it appears in the run history directly below and updates as it
goes.

Where a value comes from a set the platform already knows, the form offers the set
rather than asking you to remember the spelling. A parameter naming a connection is a
list of the connections your access reaches, each with what it is; a parameter with
declared choices is those choices. A box is for a value the platform genuinely cannot
enumerate.

A script nothing would execute has no Run control at all, for the reason stated at the
top of the page, rather than a button that fails when you press it. Editing, checking and
saving stay available, because fixing the script is how it comes back into service.

## Checking a change before you send it

Beside Run are the two things you would otherwise have had to ask an agent for.

**Validate** parses what is on screen and tells you what it would reach — which
capabilities, which connections, where it writes — and, if it does not parse, what to fix
and where. Nothing runs and nothing is saved.

**Dry run** actually executes it, as you: your identity, your access, tighter limits, and
nothing kept. Outputs are measured rather than written, so you see how many rows and how
big each one would be without a dashboard being refreshed or a file leaving the platform.
You get the log the script printed, which is usually the whole reason to have run it, and
a failure is reported with the same detail a success is.

![Dry-run a change](../images/screenshots/light/user-script-dry-run-light.webp#only-light)![Dry-run a change](../images/screenshots/dark/user-script-dry-run-dark.webp#only-dark)

Because a dry run is you running it, it reaches exactly what you reach and nothing
more. The record of the run is kept with the script, so anyone reading a version later
can see that its exact code was executed, by whom, and what it produced — and a version
nobody has dry-run says so.

## Version history

Folded into the Source section is every version of the script, each with its author and
the roles they held at the save, which are the roles a run of that version presents. It
opens on a reveal rather than standing as a section of its own: the editor above it
already holds the version that runs, so what the history adds is the versions before
that one.

![Version history](../images/screenshots/light/user-script-versions-light.webp#only-light)![Version history](../images/screenshots/dark/user-script-versions-dark.webp#only-dark)

## Run history

The run history is the refresh record of one script: what triggered each run, which
version it executed, how long it took, what it produced, and how it ended. A failure
states its reason in the list. A fire that arrived while the previous run was still
going is recorded as skipped rather than silently dropped, because a report that stopped
producing is exactly what this history has to show.

How a run ended and when it ran are read as one fact and are set as one. What triggered
it and which version executed qualify that fact rather than standing beside it — the
trigger is a short enumeration and the version is the same number down the whole history
— so they sit under it in the row rather than each holding a column open.

The section header carries what the history adds up to — the share that succeeded, how
many failed or were skipped, and the median duration — over the runs actually loaded,
which the sentence names rather than implying it covers all time.

It sits directly under the code, because an error here is answered by the text above it,
and nothing in it holds the page open sideways: a failure message wraps to as many lines
as it needs rather than running off the edge.

![Run history](../images/screenshots/light/user-script-runs-light.webp#only-light)![Run history](../images/screenshots/dark/user-script-runs-dark.webp#only-dark)

Opening a run shows what it was given, what it cost, what it wrote, and the log it
printed while working. A run has an address of its own, which is what the Runs tab links
to: following one lands on this page with that run open.

![Run log](../images/screenshots/light/user-script-run-log-light.webp#only-light)![Run log](../images/screenshots/dark/user-script-run-log-dark.webp#only-dark)

An output that went to the portal links to the asset version it produced. A recurring
script writes new versions of the same asset rather than a new asset each time, so that
asset's version history is the history of what the dashboard has been showing. An output
delivered to a bucket names where it was written and is not a link: those bytes left the
platform, and nothing here will serve them back.

The schedule controls, the source, and the run history of a script belong to its owner
and to administrators. A script you can see but do not own shows its details and what it
says about itself, and nothing else.


## Files written

Below the run history, on a script you own, is **Files written**: every portal asset and
managed resource this script has created or modified, across every run, most recently
written first. Each row names the file, its kind, how many times this script has written it, and
when it last did, and marks whether the script **created** the file or has only
**modified** it. Clicking a row opens the file.

The run history above answers what one run did, which is a different question. A script
that has run three hundred times has three hundred output lists, and a file the script
modified without declaring it as an output — through `manage_asset` or `manage_resource`
— appears in none of them. This is the list that answers what the script actually
touches, and what goes stale if you retire it.

A file the script wrote that has since been deleted stays listed, named by its id and
marked deleted: that the script wrote it is still true, and it is part of what somebody
deciding whether to retire the script has to see.

![What a script has produced](../images/screenshots/light/user-script-produced-light.webp#only-light)![What a script has produced](../images/screenshots/dark/user-script-produced-dark.webp#only-dark)

## State

Below the run history, on a script you own, is the **State** the script carries from one
run to the next: one JSON object the platform keeps for it, which a run reads as
`run.state` and saves with `platform.save_state` when it succeeds. An incremental job
keeps its watermark here, so the run after a gap continues from where the last
successful run stopped instead of recomputing a window from the fire time.

The section is folded, and its header says where the state stands: the revision and
when it last changed, or that nothing has been saved, or that the script keeps none.
Open it to read the object, the revision, and who wrote it, which is the run that saved
it or the person who last reset it. Each run in the history above states the revision it
read and, when it saved, what it wrote, so a wrong value is traced to the run that
produced it.

**Edit state** replaces the whole object; **Clear state** resets it to an empty object so
the next run starts over, which is the recovery for a wrong watermark. Both move the
revision, and a run already in flight that read the previous revision fails at its write
rather than overwriting the reset. A run that reads and saves state fails the same way
when another run of the same script wrote in between, and the failure names that run;
its outputs stand.

![Script state](../images/screenshots/light/user-script-state-light.webp#only-light)![Script state](../images/screenshots/dark/user-script-state-dark.webp#only-dark)

## Deleting a script

At the bottom of a script you own is **Delete**. It is there for the same reason
every other control on this page is: creating, editing, documenting, scheduling,
running and handing over a script all happen here, and removing one used to be
the single thing that sent you back to an agent to ask for it.

The confirmation says what goes rather than asking whether you are sure. Every
saved version of the code, including the one a run executes; the schedule, named
in words, so nothing fires it again; the whole run history; and the state the
script carried from one run to the next. A scheduled script's run history is its
refresh history — if a report has been running for months, that record is part of
what you are removing, and you should be deciding that rather than discovering
it.

It also says what stays. The assets and resources the script wrote are not the
script's to take with it: they stay where they are, owned by whoever owns them,
and they go on recording that this script wrote them, which is what **Written
by** on each of those files shows. Deleting a script is not deleting the reports
it produced.

![Deleting a script](../images/screenshots/light/user-script-delete-light.webp#only-light)![Deleting a script](../images/screenshots/dark/user-script-delete-dark.webp#only-dark)

Once the delete lands you are back at the script listing, with the script gone
from it. It cannot be undone. An administrator can delete any script, on the
same page and with the same confirmation; the alternative for anybody is
`manage_script command=delete`, which removes exactly the same things.

## Asking for the pages

Ask your agent to show you your scripts — "show me my scripts", "what scripts do I
have", "did the daily report run" — and it opens this page with the `show_scripts` tool.
That tool only opens the pages; every script operation an agent performs for its own
work uses `manage_script`, which renders nothing.

