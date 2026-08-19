import type { ScriptConnectionChoice, ScriptParam } from "@/api/portal/hooks/scripts";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

// The one control set for a script's parameters, wherever somebody supplies
// them: the schedule's bindings, a run asked for now (#1363), and a dry run of
// an edit (#1364).
//
// It exists as one component because the three surfaces are asking the same
// question — what should this parameter be for this execution — and a parameter
// rendered as a picker in one place and a text box in another is a defect the
// person using it has to notice for us.
//
// The rule the whole file follows: where the platform knows the set a value
// comes from, it offers the set. A free-text box is for values it genuinely
// cannot enumerate (#1361).

// FIRE_DATE is the one token a binding may carry. It expands at the fire, so
// the run records the date it computed for rather than the day somebody set the
// schedule. Only a schedule expands it, which is why the hint naming it is
// shown only where a schedule is being written.
export const FIRE_DATE = "${fire_date}";

// UNSET is the clearable choice in a parameter dropdown: once a value is
// picked, the placeholder is unreachable, so "leave it unbound" has to be an
// item of its own.
const UNSET = "__unset__";

// Values is a form's parameter state: strings, because that is what an input
// holds and the server coerces each one to its declared type.
export type Values = Record<string, string>;

// declaresConnection reports whether any parameter takes a connection, which is
// what decides whether a form needs to ask for the connection set at all.
export function declaresConnection(params: ScriptParam[]): boolean {
  return params.some((p) => p.type === "connection");
}

// valuesFrom seeds a form from stored bindings, rendering each as the string an
// input holds. An absent or null value is an unbound parameter rather than an
// empty one.
export function valuesFrom(stored: Record<string, unknown> | undefined): Values {
  const values: Values = {};
  for (const [name, value] of Object.entries(stored ?? {})) {
    values[name] = value === null || value === undefined ? "" : String(value);
  }
  return values;
}

// boundParams is what an execution binds. An empty box is an unbound parameter
// rather than an empty value: sending "" for a date would be refused, and a
// required one left empty is refused by the contract, which is the answer that
// names what to fix.
//
// It is driven by the CONTRACT rather than by whatever the form happens to
// carry, so a value for a parameter the contract no longer declares is dropped
// rather than sent to be rejected.
export function boundParams(params: ScriptParam[], values: Values): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const p of params) {
    const value = values[p.name];
    if (value === undefined || value === "") continue;
    out[p.name] = value;
  }
  return out;
}

// missingRequired names the required parameters still unbound, so a form can
// say what is missing instead of submitting a request it knows will be refused.
export function missingRequired(params: ScriptParam[], values: Values): string[] {
  return params
    .filter((p) => p.required && !values[p.name] && p.default === undefined)
    .map((p) => p.name);
}

interface Props {
  params: ScriptParam[];
  values: Values;
  disabled: boolean;
  onChange: (name: string, value: string) => void;
  /**
   * form distinguishes this form's controls from the other forms on the same
   * page. A script's page carries up to three of them — run now, dry run, and
   * the schedule's bindings — and a DOM id may appear once in a document: two
   * controls sharing one would make a label point at whichever came first, so
   * clicking the second form's label would move focus into the first.
   */
  form: string;
  /** The connections a connection-typed parameter may name, empty until read. */
  connections?: ScriptConnectionChoice[];
  /**
   * scheduled marks the form as writing a cadence rather than one execution,
   * which is the only context where ${fire_date} means anything.
   */
  scheduled?: boolean;
}

// ScriptParameterForm is the value supplied for each declared parameter.
export function ScriptParameterForm({
  params,
  values,
  disabled,
  onChange,
  form,
  connections,
  scheduled = false,
}: Props) {
  // A script that declares no parameters gets no form at all: the contract
  // above already says it takes none, and a second sentence saying so is one
  // more thing to read on the way to the button.
  if (params.length === 0) return null;
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      {params.map((p) => (
        <Field
          key={p.name}
          id={controlID(form, p.name)}
          label={`${p.name}${p.required ? "" : " (optional)"}`}
          hint={bindingHint(p, connections, scheduled)}
        >
          <BindingInput
            param={p}
            id={controlID(form, p.name)}
            value={values[p.name] ?? ""}
            disabled={disabled}
            connections={connections}
            scheduled={scheduled}
            onChange={(value) => onChange(p.name, value)}
          />
        </Field>
      ))}
    </div>
  );
}

// bindingHint tells the person what this box takes. For a date on a schedule it
// names the one token, since pinning the fire's own date is the reason most
// recurring reports have a date parameter at all; for a connection it says
// where the offered set came from, because "these are the ones you may pick" is
// not the same statement as "these are all of them".
export function bindingHint(
  p: ScriptParam,
  connections?: ScriptConnectionChoice[],
  scheduled = false,
): string {
  const described = p.description ? `${p.description} ` : "";
  if (p.type === "date") {
    return scheduled
      ? `${described}A date as YYYY-MM-DD, or ${FIRE_DATE} for the day the schedule fires.`
      : `${described}A date as YYYY-MM-DD.`;
  }
  if (p.type === "enum") {
    return `${described}One of: ${(p.values ?? []).join(", ")}.`;
  }
  if (p.type === "connection") {
    if (connections && connections.length === 0) {
      return `${described}No connection is available for this script to reach.`;
    }
    return `${described}A platform connection.`;
  }
  return `${described}Type: ${p.type}.`;
}

// BindingInput is the control one parameter deserves: a choice where the value
// comes from a set somebody already knows, a box otherwise.
function BindingInput({
  param,
  id,
  value,
  disabled,
  connections,
  scheduled,
  onChange,
}: {
  param: ScriptParam;
  id: string;
  value: string;
  disabled: boolean;
  connections?: ScriptConnectionChoice[];
  scheduled: boolean;
  onChange: (value: string) => void;
}) {
  const options = choicesFor(param, connections);
  if (options === null) {
    return (
      <Input
        id={id}
        value={value}
        disabled={disabled}
        placeholder={param.type === "date" && scheduled ? FIRE_DATE : ""}
        onChange={(e) => onChange(e.target.value)}
      />
    );
  }
  return (
    // The value is passed as-is, including the empty string: an unbound
    // parameter is a control with nothing selected, and handing Select an
    // undefined would make it uncontrolled until the first choice and then
    // switch, which React warns about and which loses the value on a rerender.
    <Select
      value={value}
      disabled={disabled || options.length === 0}
      onValueChange={(v) => onChange(v === UNSET ? "" : v)}
    >
      <SelectTrigger id={id} aria-label={param.name} className="w-full">
        <SelectValue placeholder={options.length === 0 ? "-- none available --" : "-- unbound --"} />
      </SelectTrigger>
      <SelectContent>
        {!param.required && <SelectItem value={UNSET}>-- unbound --</SelectItem>}
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

// Choice is one selectable value: what is bound, and what is read.
interface Choice {
  value: string;
  label: string;
}

// choicesFor is the set a parameter is chosen from, or null when there is no
// set and the answer has to be typed.
//
// A connection parameter whose set has not been read yet is deliberately still
// a picker rather than falling back to a box: a box that becomes a dropdown
// once a request lands is a control that changes under the person using it, and
// an empty picker says "nothing to pick yet" honestly.
function choicesFor(param: ScriptParam, connections?: ScriptConnectionChoice[]): Choice[] | null {
  switch (param.type) {
    case "bool":
      return [
        { value: "true", label: "true" },
        { value: "false", label: "false" },
      ];
    case "enum":
      return (param.values ?? []).map((v) => ({ value: v, label: v }));
    case "connection":
      return (connections ?? []).map((c) => ({
        value: c.name,
        label: c.description ? `${c.name} — ${c.description}` : c.name,
      }));
    default:
      return null;
  }
}

// controlID is one control's DOM id, scoped to the form it belongs to.
function controlID(form: string, param: string): string {
  return `script-param-${form}-${param}`;
}

// Field is one labeled control with the sentence that explains it.
export function Field({
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
