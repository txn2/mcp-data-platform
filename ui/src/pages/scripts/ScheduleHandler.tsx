import { useState } from "react";
import { useScriptConnections } from "@/api/portal/hooks/scripts";
import type { ScriptContract, ScriptSchedule } from "@/api/portal/hooks/scripts";
import { useSaveHandler, useSetHandlerEnabled } from "@/api/portal/hooks/scriptHandlers";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DEFAULT_CADENCE,
  describeCron,
  fromCron,
  scheduleLine,
  scheduleState,
  toCron,
  type Cadence,
} from "./cadence";
import { formatWhen } from "./runFormat";
import { ScheduleBuilder } from "./ScheduleBuilder";
import {
  boundParams,
  declaresConnection,
  ScriptParameterForm,
  valuesFrom,
  type Values,
} from "./ScriptParameterForm";

// ScheduleHandler is one of a script's schedule handlers, or a new one: where
// the owner of a script says when it runs (#1307, #2434). A script may hold
// several schedules, each firing on its own.
//
// A schedule carries no authority: the run gate and the persona filter are
// re-read at every fire, so re-timing a script cannot make it reach anything
// it could not already reach. That is why this control belongs to the person
// who owns the script rather than to an administrator, and why nothing here
// edits a version.
//
// A schedule on a script nothing will execute — disabled, retired — saves and
// stays inert, and the page says so in the gate's own words.

interface Props {
  scriptId: string;
  contract: ScriptContract;
  /** schedule is the handler shown, null for a new one. */
  schedule: ScriptSchedule | null;
  /** onDone is told when a new schedule was added. */
  onDone?: () => void;
}

export function ScheduleHandler({ scriptId, contract, schedule, onDone }: Props) {
  const params = contract.params ?? [];
  const actions = useScheduleActions(scriptId, params, schedule, onDone);
  const { current, setDraft, busy } = actions;
  // The set a connection parameter offers is what the caller's persona
  // reaches; every fire is authorized against the roles captured at the
  // script's last save (#1361).
  const { data: connections } = useScriptConnections(scriptId, declaresConnection(params));
  // Each schedule's controls carry its own ids: a page can show a schedule
  // being added beside one being edited (#2434).
  const form = `schedule-${schedule?.id ?? "new"}`;

  return (
    <div className="space-y-4">
      <ScheduleHeader schedule={schedule} busy={busy} onToggle={actions.togglePause} />
      <InertNotice contract={contract} scheduled={!!schedule} />
      <form className="space-y-4" onSubmit={actions.submit}>
        <ScheduleBuilder
          idPrefix={form}
          cadence={current.cadence}
          timezone={current.timezone}
          busy={busy}
          saved={schedule && { cron: schedule.cron_spec, timezone: schedule.timezone }}
          onCadenceChange={(cadence) => setDraft({ ...current, cadence })}
          onTimezoneChange={(timezone) => setDraft({ ...current, timezone })}
        />

        <ScriptParameterForm
          form={form}
          params={params}
          values={current.values}
          disabled={busy}
          connections={connections?.data}
          scheduled
          onChange={(name, value) =>
            setDraft({ ...current, values: { ...current.values, [name]: value } })
          }
        />

        {actions.error && (
          <Alert variant="destructive">
            <AlertDescription>{actions.error}</AlertDescription>
          </Alert>
        )}

        <FormActions
          adding={!schedule}
          busy={busy}
          empty={toCron(current.cadence) === ""}
          onCancel={onDone}
        />
      </form>
    </div>
  );
}

// useScheduleActions is the schedule's draft and the two writes made on it:
// saving the cadence, which adds a new schedule or changes the one shown, and
// pausing or resuming it.
function useScheduleActions(
  scriptId: string,
  params: NonNullable<ScriptContract["params"]>,
  schedule: ScriptSchedule | null,
  onDone?: () => void,
) {
  const save = useSaveHandler(scriptId, schedule?.id);
  const pause = useSetHandlerEnabled(schedule?.id ?? "");
  // draft holds the owner's edits and nothing else, so a background refetch
  // cannot discard a schedule somebody is part-way through typing.
  const [draft, setDraft] = useState<Draft | null>(null);
  const [error, setError] = useState<string | null>(null);

  const current = draft ?? draftOf(schedule);
  const busy = save.isPending || pause.isPending;
  const fail = (e: unknown) => setError(e instanceof Error ? e.message : "The change could not be saved");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setError(null);
    save.mutate(
      {
        kind: "schedule",
        // The builder is the input; cron is what the platform stores. The
        // expression is derived here rather than typed, except on the custom
        // choice, which IS the expression.
        cron: toCron(current.cadence),
        timezone: current.timezone.trim(),
        args: boundParams(params, current.values),
      },
      {
        onError: fail,
        onSuccess: () => {
          setDraft(null);
          onDone?.();
        },
      },
    );
  };

  const togglePause = () => {
    setError(null);
    pause.mutate(!schedule?.enabled, { onError: fail });
  };

  return { current, setDraft, busy, error, submit, togglePause };
}

// ScheduleHeader is the schedule in force and its pause control, nothing for
// a schedule not added yet.
function ScheduleHeader({
  schedule,
  busy,
  onToggle,
}: {
  schedule: ScriptSchedule | null;
  busy: boolean;
  onToggle: () => void;
}) {
  if (!schedule) return null;
  return (
    <div className="flex flex-wrap items-start justify-between gap-2">
      <ScheduleSummary schedule={schedule} />
      <PauseButton schedule={schedule} busy={busy} onToggle={onToggle} />
    </div>
  );
}

// FormActions is the save, worded for adding a schedule or changing one, and
// a Cancel for an addition.
function FormActions({
  adding,
  busy,
  empty,
  onCancel,
}: {
  adding: boolean;
  busy: boolean;
  empty: boolean;
  onCancel?: () => void;
}) {
  return (
    <div className="flex items-center gap-3">
      <Button type="submit" size="sm" disabled={busy || empty}>
        {adding ? "Add schedule" : "Update schedule"}
      </Button>
      {adding && onCancel && (
        <Button type="button" size="sm" variant="ghost" disabled={busy} onClick={onCancel}>
          Cancel
        </Button>
      )}
    </div>
  );
}

// scheduleHeadline is what the folded section says in place of the builder:
// what this script does now, in the words the listing states it in, so the two
// surfaces cannot describe the same cadence differently. A paused schedule is
// named as paused rather than as what it would do, because what it does is
// nothing.
export function scheduleHeadline(schedule: ScriptSchedule | null): string {
  if (!schedule) return "Not scheduled";
  const line = scheduleLine(schedule.cron_spec, schedule.timezone);
  return schedule.enabled ? `Runs: ${line}` : `Paused: ${line}`;
}

// PauseButton is the retirement control, absent until there is a schedule to
// retire. A schedule that produced runs cannot be deleted, because it is part
// of the explanation of those runs; it is paused instead.
function PauseButton({
  schedule,
  busy,
  onToggle,
}: {
  schedule: ScriptSchedule | null;
  busy: boolean;
  onToggle: () => void;
}) {
  if (!schedule) return null;
  return (
    <Button variant="outline" size="sm" onClick={onToggle} disabled={busy}>
      {schedule.enabled ? "Pause" : "Resume"}
    </Button>
  );
}

// Draft is the form's own state: the schedule as the builder holds it, and the
// bindings as strings, because that is what an input holds and the server
// coerces each one to its declared type.
interface Draft {
  cadence: Cadence;
  timezone: string;
  values: Values;
}

// draftOf seeds the form from the stored schedule, so editing one starts from
// the schedule in force rather than from an empty form that would silently drop
// the bindings it does not carry. An expression the builder cannot express
// comes back as the custom choice carrying it verbatim.
function draftOf(schedule: ScriptSchedule | null): Draft {
  return {
    cadence: schedule ? fromCron(schedule.cron_spec) : DEFAULT_CADENCE,
    timezone: schedule?.timezone || defaultTimezone(),
    values: valuesFrom(schedule?.params),
  };
}

// defaultTimezone is the reader's own zone for a schedule being set for the
// first time. A person setting a 7am report almost always means 7am where they
// are; the platform still stores UTC when the runtime cannot name a zone.
function defaultTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

// ScheduleSummary is the schedule in force, stated before the form that changes
// it: what it does now, when it fires next, and whether it has been missing
// fires — the only place a gap in a script's schedule is visible.
function ScheduleSummary({ schedule }: { schedule: ScriptSchedule }) {
  const state = summaryState(schedule);
  return (
    <div className="space-y-1 text-sm">
      {/* The schedule in force, in words. The expression is underneath it for
          anyone who reads cron, rather than in front of everyone who does not. */}
      <p>
        {describeCron(schedule.cron_spec, schedule.timezone)} — {state}.
      </p>
      <p className="font-mono text-xs text-muted-foreground">{schedule.cron_spec}</p>
      {!!schedule.missed_fires && (
        <p className="text-xs text-muted-foreground">
          {schedule.missed_fires} fire{schedule.missed_fires === 1 ? " has" : "s have"} been
          missed. A gap is never caught up on: the platform runs the most recent fire once and
          counts the rest.
        </p>
      )}
    </div>
  );
}

// summaryState is what the schedule is doing, in the clause this page has room
// for. Which state it is in is decided by scheduleState, which the scripts
// listing reads too, so the two surfaces cannot disagree about whether a
// schedule is going to fire while phrasing it for the space each has.
function summaryState(schedule: ScriptSchedule): string {
  const state = scheduleState(schedule);
  switch (state.kind) {
    case "paused":
      return "paused, and firing nothing until it is resumed";
    case "idle":
      return "enabled, with no further fire due";
    case "due":
      return `next fire ${formatWhen(state.at)}`;
  }
}

// InertNotice says that a schedule on this script fires nothing. It is driven
// by the run gate's own refusal rather than by a second reading of it, so the
// page cannot disagree with what run_script would answer; the refusal itself
// is stated once, at the top of the page, and repeating the sentence here
// would only make it easier to end up with two of them.
function InertNotice({ contract, scheduled }: { contract: ScriptContract; scheduled: boolean }) {
  if (!contract.refusal) return null;
  return (
    <Alert>
      <AlertDescription>
        {scheduled
          ? "This schedule is saved, but nothing will execute it, for the reason stated above."
          : "A schedule set here saves, and stays inert: nothing will execute it, for the reason stated above."}
      </AlertDescription>
    </Alert>
  );
}
