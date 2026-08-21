import { useState } from "react";
import {
  useScriptConnections,
  useScriptSchedule,
  useSetScriptSchedule,
  useSetScriptSchedulePaused,
} from "@/api/portal/hooks/scripts";
import type { ScriptContract, ScriptSchedule } from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  DEFAULT_CADENCE,
  describeCron,
  fromCron,
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

// ScriptScheduleEditor is where the owner of an automation says when it runs
// (#1307). It is the only thing on these pages that changes anything.
//
// A cadence carries no authority: the run gate and the persona filter are
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
}

export function ScriptScheduleEditor({ scriptId, contract }: Props) {
  const { data: schedule, isLoading, error } = useScriptSchedule(scriptId, true);
  if (isLoading) {
    return (
      <SectionCard title="Schedule">
        <p className="text-sm text-muted-foreground">Loading the schedule...</p>
      </SectionCard>
    );
  }
  // A cadence that could not be read is not offered for editing: the form would
  // be empty, and saving it would replace a schedule nobody could see.
  if (error) {
    return (
      <SectionCard title="Schedule">
        <p className="text-sm text-muted-foreground">
          This script's schedule could not be read, so it cannot be changed here.
        </p>
      </SectionCard>
    );
  }
  // Keyed on the script so a part-typed cadence cannot survive onto a different
  // script: this component sits at the same position in the tree for every
  // script, and React would otherwise keep the draft across the change.
  return (
    <ScheduleControls
      key={scriptId}
      scriptId={scriptId}
      contract={contract}
      schedule={schedule ?? null}
    />
  );
}

// ScheduleControls is the cadence in force and the form that changes it. It is
// separate from the loading and unreadable states so that this component always
// has a schedule (or a definite absence of one) to work from.
function ScheduleControls({
  scriptId,
  contract,
  schedule,
}: Props & { schedule: ScriptSchedule | null }) {
  const save = useSetScriptSchedule(scriptId);
  const pause = useSetScriptSchedulePaused(scriptId);
  // The set a connection parameter offers is what the caller's persona
  // reaches; every fire is authorized against the roles captured at the
  // script's last save (#1361).
  const { data: connections } = useScriptConnections(
    scriptId,
    declaresConnection(contract.params ?? []),
  );
  // draft holds the owner's edits and nothing else, so a background refetch
  // cannot discard a cadence somebody is part-way through typing.
  const [draft, setDraft] = useState<Draft | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);

  const current = draft ?? draftOf(schedule);
  const busy = save.isPending || pause.isPending;
  const fail = (e: unknown) =>
    setActionError(e instanceof Error ? e.message : "The change could not be saved");

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    setActionError(null);
    save.mutate(
      {
        // The builder is the input; cron is what the platform stores. The
        // expression is derived here rather than typed, except on the custom
        // cadence, which IS the expression.
        cron: toCron(current.cadence),
        timezone: current.timezone.trim(),
        params: boundParams(contract.params ?? [], current.values),
      },
      { onError: fail, onSuccess: () => setDraft(null) },
    );
  };

  const togglePause = () => {
    setActionError(null);
    pause.mutate(!schedule?.enabled, { onError: fail });
  };

  return (
    <SectionCard
      title="Schedule"
      action={<PauseButton schedule={schedule} busy={busy} onToggle={togglePause} />}
    >
      <div className="space-y-4">
        <CadenceSummary schedule={schedule} />
        <InertNotice contract={contract} scheduled={!!schedule} />
        <form className="space-y-4" onSubmit={submit}>
          <ScheduleBuilder
            cadence={current.cadence}
            timezone={current.timezone}
            busy={busy}
            saved={schedule ? { cron: schedule.cron_spec, timezone: schedule.timezone } : null}
            onCadenceChange={(cadence) => setDraft({ ...current, cadence })}
            onTimezoneChange={(timezone) => setDraft({ ...current, timezone })}
          />

          <ScriptParameterForm
            form="schedule"
            params={contract.params ?? []}
            values={current.values}
            disabled={busy}
            connections={connections?.data}
            scheduled
            onChange={(name, value) =>
              setDraft({ ...current, values: { ...current.values, [name]: value } })
            }
          />

          {actionError && (
            <Alert variant="destructive">
              <AlertDescription>{actionError}</AlertDescription>
            </Alert>
          )}

          <div className="flex items-center gap-3">
            <Button type="submit" size="sm" disabled={busy || toCron(current.cadence) === ""}>
              {schedule ? "Update schedule" : "Set schedule"}
            </Button>
            <p className="text-xs text-muted-foreground">
              Changing the cadence does not change what runs: every fire executes the
              latest saved version, under the access captured at that save.
            </p>
          </div>
        </form>
      </div>
    </SectionCard>
  );
}

// PauseButton is the retirement control, absent until there is a schedule to
// retire. There is deliberately no delete: a schedule that produced runs is
// part of the explanation of those runs.
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

// Draft is the form's own state: the cadence as the builder holds it, and the
// bindings as strings, because that is what an input holds and the server
// coerces each one to its declared type.
interface Draft {
  cadence: Cadence;
  timezone: string;
  values: Values;
}

// draftOf seeds the form from the stored schedule, so editing a cadence starts
// from the one in force rather than from an empty form that would silently drop
// the bindings it does not carry. An expression the builder cannot express
// comes back as the custom cadence carrying it verbatim.
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

// CadenceSummary is the schedule in force, stated before the form that changes
// it: what it does now, when it fires next, and whether it has been missing
// fires — the only place a gap in an automation is visible.
function CadenceSummary({ schedule }: { schedule: ScriptSchedule | null }) {
  if (!schedule) {
    return (
      <p className="text-sm text-muted-foreground">
        This script runs only when someone asks for one. Give it a cadence below and the
        platform will run it unattended.
      </p>
    );
  }
  const state = cadenceState(schedule);
  return (
    <div className="space-y-1 text-sm">
      {/* The cadence in force, in words. The expression is underneath it for
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

// cadenceState is what the schedule is doing, in the clause this page has room
// for. Which state it is in is decided by scheduleState, which the scripts
// listing reads too, so the two surfaces cannot disagree about whether a
// cadence is going to fire while phrasing it for the space each has.
function cadenceState(schedule: ScriptSchedule): string {
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

// InertNotice says that a cadence on this script fires nothing. It is driven
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
