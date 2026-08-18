import { useState } from "react";
import {
  useScriptSchedule,
  useSetScriptSchedule,
  useSetScriptSchedulePaused,
} from "@/api/portal/hooks/scripts";
import type {
  ScriptContract,
  ScriptParam,
  ScriptSchedule,
} from "@/api/portal/hooks/scripts";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
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

// ScriptScheduleEditor is where the owner of an automation says when it runs
// (#1307). It is the only thing on these pages that changes anything.
//
// A cadence carries no authority: the execution gate and the capability grant
// are re-read at every fire, so re-timing a script cannot make it reach
// anything it could not already reach. That is why this control belongs to the
// person who owns the script rather than to an administrator, and why nothing
// here approves, rejects, or edits a version.
//
// The one thing it must never do is imply an approval it cannot grant. A
// schedule on a script nothing will execute saves and stays inert, and the page
// says so in the gate's own words.

// FIRE_DATE is the one token a binding may carry. It expands at the fire, so
// the run records the date it computed for rather than the day somebody set the
// schedule.
const FIRE_DATE = "${fire_date}";

// UNSET is the clearable choice in a parameter dropdown: once a value is
// picked, the placeholder is unreachable, so "leave it unbound" has to be an
// item of its own.
const UNSET = "__unset__";

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

          <ParameterBindings
            params={contract.params ?? []}
            values={current.values}
            disabled={busy}
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
              Changing the cadence does not change what runs: the approved version and the
              capabilities it holds are the same before and after.
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
  values: Record<string, string>;
}

// draftOf seeds the form from the stored schedule, so editing a cadence starts
// from the one in force rather than from an empty form that would silently drop
// the bindings it does not carry. An expression the builder cannot express
// comes back as the custom cadence carrying it verbatim.
function draftOf(schedule: ScriptSchedule | null): Draft {
  const values: Record<string, string> = {};
  for (const [name, value] of Object.entries(schedule?.params ?? {})) {
    values[name] = value === null || value === undefined ? "" : String(value);
  }
  return {
    cadence: schedule ? fromCron(schedule.cron_spec) : DEFAULT_CADENCE,
    timezone: schedule?.timezone || defaultTimezone(),
    values,
  };
}

// defaultTimezone is the reader's own zone for a schedule being set for the
// first time. A person setting a 7am report almost always means 7am where they
// are; the platform still stores UTC when the runtime cannot name a zone.
function defaultTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

// boundParams is what the fire binds. An empty box is an unbound parameter
// rather than an empty value: sending "" for a date would be refused, and a
// required one left empty is refused by the contract, which is the answer that
// names what to fix.
//
// It is driven by the CONTRACT rather than by what the schedule happens to
// carry, so a binding for a parameter the approved version no longer declares
// is dropped on the next save. That binding is already failing every fire —
// the contract refuses it — so saving through this form repairs the schedule
// rather than preserving the thing that broke it.
function boundParams(
  params: ScriptParam[],
  values: Record<string, string>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const p of params) {
    const value = values[p.name];
    if (value === undefined || value === "") continue;
    out[p.name] = value;
  }
  return out;
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

// InertNotice says that a cadence on this script fires nothing. It is driven by
// the execution gate's own refusal rather than by a second reading of the
// approval, so the page cannot disagree with what run_script would answer; the
// refusal itself is stated once, at the top of the page, and repeating the
// sentence here would only make it easier to end up with two of them.
function InertNotice({ contract, scheduled }: { contract: ScriptContract; scheduled: boolean }) {
  if (!contract.approval.refusal) return null;
  return (
    <Alert>
      <AlertDescription>
        {scheduled
          ? "This schedule is saved, but nothing will execute it, for the reason stated above."
          : "A schedule set here saves, and stays inert: nothing will execute it, for the reason stated above."}{" "}
        Approving a version is an administrator's decision, and setting a cadence does not ask
        for one.
      </AlertDescription>
    </Alert>
  );
}

// ParameterBindings is the value every fire passes for each declared
// parameter. The contract shown is the approved version's, because that is the
// version anything will execute.
function ParameterBindings({
  params,
  values,
  disabled,
  onChange,
}: {
  params: ScriptParam[];
  values: Record<string, string>;
  disabled: boolean;
  onChange: (name: string, value: string) => void;
}) {
  // A script that declares no parameters gets no bindings section at all: the
  // contract above already says it takes none, and a second sentence saying so
  // is one more thing to read on the way to the save button.
  if (params.length === 0) return null;
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      {params.map((p) => (
        <Field
          key={p.name}
          id={`script-param-${p.name}`}
          label={`${p.name}${p.required ? "" : " (optional)"}`}
          hint={bindingHint(p)}
        >
          <BindingInput
            param={p}
            value={values[p.name] ?? ""}
            disabled={disabled}
            onChange={(value) => onChange(p.name, value)}
          />
        </Field>
      ))}
    </div>
  );
}

// bindingHint tells the owner what this box takes, and — for a date — what the
// one token means, since pinning the fire's own date is the reason most
// recurring reports have a date parameter at all.
function bindingHint(p: ScriptParam): string {
  const described = p.description ? `${p.description} ` : "";
  if (p.type === "date") {
    return `${described}A date as YYYY-MM-DD, or ${FIRE_DATE} for the day the schedule fires.`;
  }
  if (p.type === "enum") {
    return `${described}One of: ${(p.values ?? []).join(", ")}.`;
  }
  return `${described}Type: ${p.type}.`;
}

// BindingInput is the control one parameter deserves: a choice where the
// contract declares one, a box otherwise.
function BindingInput({
  param,
  value,
  disabled,
  onChange,
}: {
  param: ScriptParam;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const options = param.type === "bool" ? ["true", "false"] : (param.values ?? []);
  if (param.type !== "bool" && param.type !== "enum") {
    return (
      <Input
        id={`script-param-${param.name}`}
        value={value}
        disabled={disabled}
        placeholder={param.type === "date" ? FIRE_DATE : ""}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  return (
    <Select
      value={value === "" ? undefined : value}
      disabled={disabled}
      onValueChange={(v) => onChange(v === UNSET ? "" : v)}
    >
      <SelectTrigger id={`script-param-${param.name}`} aria-label={param.name} className="w-full">
        <SelectValue placeholder="-- unbound --" />
      </SelectTrigger>
      <SelectContent>
        {!param.required && <SelectItem value={UNSET}>-- unbound --</SelectItem>}
        {options.map((o) => (
          <SelectItem key={o} value={o}>
            {o}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// Field is one labeled control with the sentence that explains it.
function Field({
  id,
  label,
  hint,
  children,
}: {
  id: string;
  label: string;
  hint: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id}>{label}</Label>
      {children}
      <p className="text-xs text-muted-foreground">{hint}</p>
    </div>
  );
}
