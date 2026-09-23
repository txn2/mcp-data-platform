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
    values[name] = formValue(value);
  }
  return values;
}

// formValue renders one stored binding as the string a control holds: a list
// as comma-separated values, a date range as from and to joined by RANGE_SEP.
function formValue(value: unknown): string {
  if (value === null || value === undefined) return "";
  if (Array.isArray(value)) return value.map(String).join(", ");
  if (typeof value === "object") {
    const range = value as { from?: unknown; to?: unknown };
    return `${String(range.from ?? "")}${RANGE_SEP}${String(range.to ?? "")}`;
  }
  return String(value);
}

// RANGE_SEP joins a date range's two ends in the one string a form holds.
const RANGE_SEP = "..";

// listValues splits a list control's text into its values: separated by
// commas or new lines, trimmed, empties dropped.
export function listValues(text: string): string[] {
  return text
    .split(/[,\n]/)
    .map((v) => v.trim())
    .filter((v) => v !== "");
}

// wireValue is what one parameter's form text is sent as: a list as an
// array, a date range as {from, to}, and every other type as the string the
// server coerces (#1844).
function wireValue(p: ScriptParam, text: string): unknown {
  if (p.type === "list") return listValues(text);
  if (p.type === "date_range") {
    const [from = "", to = ""] = text.split(RANGE_SEP);
    return { from, to };
  }
  return text;
}

// orderedParams is the order a form shows the parameters in: grouped, then by
// declared order, then as declared (#1844). A parameter bound to the caller
// (#1846) has no field: its value is the caller's, and a value sent for it is
// refused.
export function orderedParams(params: ScriptParam[]): ScriptParam[] {
  return params
    .filter((p) => !p.bind)
    .map((p, i) => ({ p, i }))
    .sort((a, b) => (a.p.order ?? 0) - (b.p.order ?? 0) || a.i - b.i)
    .map(({ p }) => p);
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
    if (p.bind) continue;
    const value = values[p.name];
    if (value === undefined || value === "" || value === RANGE_SEP) continue;
    out[p.name] = wireValue(p, value);
  }
  return out;
}

// missingRequired names the required parameters still unbound, so a form can
// say what is missing instead of submitting a request it knows will be refused.
export function missingRequired(params: ScriptParam[], values: Values): string[] {
  return params
    .filter((p) => !p.bind && p.required && !values[p.name] && p.default === undefined)
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
  const shown = orderedParams(params);
  if (shown.length === 0) return null;
  return (
    <div className="space-y-4">
      {paramGroups(shown).map(({ group, members }) => (
        <div key={group || "__ungrouped__"} className="space-y-2">
          {group && <h4 className="text-sm font-medium">{group}</h4>}
          <div className="grid gap-4 sm:grid-cols-2">
            {members.map((p) => (
              <Field
                key={p.name}
                id={controlID(form, p.name)}
                label={`${p.label || p.name}${p.required ? "" : " (optional)"}`}
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
        </div>
      ))}
    </div>
  );
}

// paramGroups splits ordered parameters into their groups, keeping the order
// each group first appears in; parameters with no group come first.
function paramGroups(params: ScriptParam[]): { group: string; members: ScriptParam[] }[] {
  const groups: { group: string; members: ScriptParam[] }[] = [];
  for (const p of params) {
    const name = p.group ?? "";
    let g = groups.find((x) => x.group === name);
    if (!g) {
      g = { group: name, members: [] };
      if (name === "") groups.unshift(g);
      else groups.push(g);
    }
    g.members.push(p);
  }
  return groups;
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
  const hint = TYPE_HINTS[p.type];
  return `${described}${hint ? hint(p, connections, scheduled) : `Type: ${p.type}.`}`;
}

// TYPE_HINTS is what each type's control takes, in the words its hint uses.
const TYPE_HINTS: Record<
  string,
  (p: ScriptParam, connections: ScriptConnectionChoice[] | undefined, scheduled: boolean) => string
> = {
  date: (_p, _c, scheduled) =>
    scheduled ? `A date as YYYY-MM-DD, or ${FIRE_DATE} for the day the schedule fires.` : "A date as YYYY-MM-DD.",
  enum: (p) => `One of: ${(p.values ?? []).join(", ")}.`,
  connection: (_p, connections) =>
    connections && connections.length === 0
      ? "No connection is available for this script to reach."
      : "A platform connection.",
  list: (p) =>
    p.items === "enum" ? "Any of the values checked." : `Several ${p.items ?? "string"} values, separated by commas.`,
  date_range: () => "A range of dates, from and to as YYYY-MM-DD.",
};

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
  const composite = compositeInput({ param, id, value, disabled, onChange });
  if (composite) return composite;
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

// compositeInput is the control for a value that is more than one scalar: a
// date range's two dates, or a list of enum's checkboxes. Null otherwise.
function compositeInput({
  param,
  id,
  value,
  disabled,
  onChange,
}: {
  param: ScriptParam;
  id: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}): React.ReactNode {
  if (param.type === "date_range") {
    return <RangeInput id={id} value={value} disabled={disabled} onChange={onChange} />;
  }
  if (param.type === "list" && param.items === "enum") {
    return (
      <EnumListInput id={id} values={param.values ?? []} value={value} disabled={disabled} onChange={onChange} />
    );
  }
  return null;
}

// RangeInput is a date range: two dates, held as one string.
function RangeInput({
  id,
  value,
  disabled,
  onChange,
}: {
  id: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const [from = "", to = ""] = value.split(RANGE_SEP);
  return (
    <div className="flex items-center gap-2">
      <Input
        id={id}
        aria-label="from"
        placeholder="YYYY-MM-DD"
        value={from}
        disabled={disabled}
        onChange={(e) => onChange(`${e.target.value}${RANGE_SEP}${to}`)}
      />
      <span className="text-xs text-muted-foreground">to</span>
      <Input
        aria-label="to"
        placeholder="YYYY-MM-DD"
        value={to}
        disabled={disabled}
        onChange={(e) => onChange(`${from}${RANGE_SEP}${e.target.value}`)}
      />
    </div>
  );
}

// EnumListInput is a list of enum: one checkbox per allowed value, held as the
// comma-separated values checked.
function EnumListInput({
  id,
  values,
  value,
  disabled,
  onChange,
}: {
  id: string;
  values: string[];
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const checked = new Set(listValues(value));
  return (
    <div id={id} className="flex flex-wrap gap-3">
      {values.map((v) => (
        <label key={v} className="flex items-center gap-1.5 text-sm">
          <input
            type="checkbox"
            checked={checked.has(v)}
            disabled={disabled}
            onChange={(e) => {
              const next = values.filter((x) => (x === v ? e.target.checked : checked.has(x)));
              onChange(next.join(", "));
            }}
          />
          {v}
        </label>
      ))}
    </div>
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
