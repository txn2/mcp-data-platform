import { useState } from "react";
import { Plus, X } from "lucide-react";
import type { CallCondition, CallHandlerDetail } from "@/api/portal/hooks/callHandlers";
import { useSaveHandler, type CallHandlerKind } from "@/api/portal/hooks/scriptHandlers";
import { useScriptContract, type ScriptParam } from "@/api/portal/hooks/scripts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ChipInput } from "@/pages/settings/ChipInput";

// CallHandlerForm adds or changes one tool-call handler (#2436, #2634, #2635):
// a policy, a transform or a watch on a connection, its match (who, which
// operations, and argument conditions), and its kind's settings. The same
// form is reached from a script's page (the connection chosen here) and from
// a connection's page (the script chosen there), so one handler service and
// one set of words serve both. Only administrators reach it; the server
// refuses anyone else.

export const KIND_WORDS: Record<CallHandlerKind, { label: string; hint: string; entry: string }> = {
  policy: { label: "Policy", hint: "Decide whether a call runs", entry: "decide(call)" },
  transform: { label: "Transform", hint: "Change what a call returns", entry: "transform(call, result)" },
  watch: { label: "Watch", hint: "Run a script after a call", entry: "main(), reading run.call" },
};

type Who = "everyone" | "personas" | "users";

// whoOf is who an existing handler applies to.
function whoOf(h?: CallHandlerDetail): Who {
  if ((h?.users ?? []).length > 0) return "users";
  if ((h?.personas ?? []).length > 0) return "personas";
  return "everyone";
}

export interface CallHandlerFormProps {
  scriptId: string;
  kind: CallHandlerKind;
  /** connection fixes the connection, as the connection's page does. */
  connection?: string;
  /** handler is the handler being changed, absent when adding one. */
  handler?: CallHandlerDetail;
  /** approval is the script's version state, for the approve offer. */
  approval?: { latest: number; running: number };
  onDone: () => void;
}

// FormState is everything the form edits.
interface FormState {
  conn: string;
  who: Who;
  personas: string[];
  users: string[];
  operations: string[];
  conditions: CallCondition[];
  itemsPath: string;
  timeoutMS: string;
  onFailure: string;
  protectedCols: string[];
  args: Record<string, string>;
  approve: boolean;
}

// initialState is the form for a handler being changed, or a new one.
function initialState(kind: CallHandlerKind, connection: string | undefined, handler?: CallHandlerDetail, approval?: { latest: number; running: number }): FormState {
  if (!handler) return blankState(kind, connection ?? "");
  return fromHandler(handler, Boolean(approval && approval.latest > approval.running));
}

// blankState is a new handler's form: every call, everyone, the defaults.
function blankState(kind: CallHandlerKind, conn: string): FormState {
  return {
    conn,
    who: "everyone",
    personas: [],
    users: [],
    operations: [],
    conditions: [],
    itemsPath: "",
    timeoutMS: "2000",
    onFailure: kind === "transform" ? "fail" : "hold",
    protectedCols: [],
    args: {},
    approve: true,
  };
}

// fromHandler is a handler's form as it is stored.
function fromHandler(h: CallHandlerDetail, waiting: boolean): FormState {
  return {
    conn: h.connection,
    who: whoOf(h),
    personas: list(h.personas),
    users: list(h.users),
    operations: list(h.operations),
    conditions: h.conditions,
    itemsPath: h.items_path,
    timeoutMS: String(h.timeout_ms),
    onFailure: h.on_failure,
    protectedCols: list(h.protected),
    args: argsText(h.args),
    approve: waiting,
  };
}

function list(v?: string[] | null): string[] {
  return v ?? [];
}

// inputOf is what the form sends: the match, and the settings of its kind.
export function inputOf(kind: CallHandlerKind, st: FormState, params: ScriptParam[]): Record<string, unknown> {
  const input: Record<string, unknown> = {
    kind,
    connection: st.conn.trim(),
    operations: st.operations,
    personas: st.who === "personas" ? st.personas : [],
    users: st.who === "users" ? st.users : [],
    conditions: st.conditions.filter((c) => c.name.trim() !== ""),
    timeout_ms: Number(st.timeoutMS) || undefined,
    args: typedArgs(st.args, params),
    approve: st.approve,
  };
  if (kind === "policy") input.items_path = st.itemsPath.trim();
  if (kind !== "watch") input.on_failure = st.onFailure;
  if (kind === "transform") input.protected = st.protectedCols;
  return input;
}

export function CallHandlerForm({ scriptId, kind, connection, handler, approval, onDone }: CallHandlerFormProps) {
  const save = useSaveHandler(scriptId, handler?.handler);
  const params = useScriptContract(scriptId).data?.contract.params ?? [];
  const [st, setSt] = useState<FormState>(() => initialState(kind, connection, handler, approval));
  const set = (patch: Partial<FormState>) => setSt((cur) => ({ ...cur, ...patch }));
  const [problem, setProblem] = useState("");

  const submit = async () => {
    setProblem("");
    if (!st.conn.trim()) {
      setProblem("Name the connection the handler watches.");
      return;
    }
    try {
      await save.mutateAsync(inputOf(kind, st, params));
      onDone();
    } catch (e: unknown) {
      setProblem(e instanceof Error ? e.message : "The handler could not be saved. Nothing was changed.");
    }
  };

  return (
    <div className="space-y-4" data-testid={`call-handler-form-${kind}`}>
      {connection === undefined && (
        <Field label="Connection" htmlFor="ch-connection" hint="A connection's name, or a pattern like billing-*.">
          <Input id="ch-connection" value={st.conn} onChange={(e) => set({ conn: e.target.value })} />
        </Field>
      )}
      {approval && (
        <VersionOffer kind={kind} approval={approval} approve={st.approve} onApprove={(approve) => set({ approve })} adding={!handler} />
      )}
      <ParamsFields params={params} args={st.args} onChange={(args) => set({ args })} />
      <WhoFields st={st} set={set} />
      <div className="space-y-2">
        <p className="text-sm font-medium">Operations</p>
        <ListField
          label="Add an operation"
          placeholder="An operation, or a pattern like POST *"
          values={st.operations}
          onChange={(operations) => set({ operations })}
        />
        <p className="text-xs text-muted-foreground">
          An operation id from the connection&apos;s catalog, a tool name, or a pattern by method and path. None: every
          call on the connection.
        </p>
      </div>
      <Conditions conditions={st.conditions} onChange={(conditions) => set({ conditions })} />
      <KindSettings kind={kind} st={st} set={set} />
      {problem && (
        <Alert variant="destructive">
          <AlertDescription>{problem}</AlertDescription>
        </Alert>
      )}
      <div className="flex justify-end gap-2 border-t pt-3">
        <Button variant="outline" size="sm" onClick={onDone} disabled={save.isPending}>
          Cancel
        </Button>
        <Button size="sm" onClick={() => void submit()} disabled={save.isPending}>
          {handler ? "Save handler" : "Add handler"}
        </Button>
      </div>
    </div>
  );
}

type SetState = (patch: Partial<FormState>) => void;

// ParamsFields are the script's parameters, the handler's arguments to it.
function ParamsFields({ params, args, onChange }: { params: ScriptParam[]; args: Record<string, string>; onChange: (a: Record<string, string>) => void }) {
  if (params.length === 0) return null;
  return (
    <div className="space-y-2">
      <p className="text-sm font-medium">Script parameters</p>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
        {params.map((p) => (
          <Field key={p.name} label={p.description ? `${p.name}: ${p.description}` : p.name} htmlFor={`ch-arg-${p.name}`} small>
            <Input
              id={`ch-arg-${p.name}`}
              value={args[p.name] ?? defaultText(p)}
              onChange={(e) => onChange({ ...args, [p.name]: e.target.value })}
            />
          </Field>
        ))}
      </div>
    </div>
  );
}

function defaultText(p: ScriptParam): string {
  return p.default === undefined ? "" : String(p.default);
}

const WHO_LABELS: Record<Who, string> = { everyone: "Everyone", personas: "Personas", users: "People" };

// WhoFields are who the handler applies to: everyone, personas, or people.
function WhoFields({ st, set }: { st: FormState; set: SetState }) {
  return (
    <fieldset className="space-y-2">
      <legend className="text-sm font-medium">Applies to</legend>
      <div className="flex flex-wrap gap-4 text-sm">
        {(Object.keys(WHO_LABELS) as Who[]).map((w) => (
          <label key={w} className="flex items-center gap-2">
            <input type="radio" name="ch-who" checked={st.who === w} onChange={() => set({ who: w })} />
            {WHO_LABELS[w]}
          </label>
        ))}
      </div>
      {st.who === "personas" && (
        <ListField label="Add a persona" placeholder="Add a persona" values={st.personas} onChange={(personas) => set({ personas })} />
      )}
      {st.who === "users" && (
        <ListField label="Add a person" placeholder="name@example.com" values={st.users} onChange={(users) => set({ users })} lower />
      )}
      <p className="text-xs text-muted-foreground">It only ever sees calls these people may already make.</p>
    </fieldset>
  );
}

// ListField edits a list of short strings, holding its own draft.
function ListField({
  label,
  placeholder,
  values,
  onChange,
  lower,
}: {
  label: string;
  placeholder: string;
  values: string[];
  onChange: (v: string[]) => void;
  lower?: boolean;
}) {
  const [draft, setDraft] = useState("");
  return (
    <ChipInput
      label={label}
      placeholder={placeholder}
      values={values}
      draft={draft}
      onDraftChange={setDraft}
      onAdd={(v) => onChange([...values, lower ? v.toLowerCase() : v])}
      onRemove={(v) => onChange(values.filter((x) => x !== v))}
    />
  );
}

// KindSettings are the settings of the handler's kind: a policy's item count,
// a transform's protected columns, the time limit and failure mode, and what a
// transform or a watch should be read as.
function KindSettings({ kind, st, set }: { kind: CallHandlerKind; st: FormState; set: SetState }) {
  return (
    <>
      {kind === "policy" && (
        <Field label="Items in a call" htmlFor="ch-items" hint="The body field whose length is the item count. Empty: one item per call.">
          <Input id="ch-items" className="font-mono" value={st.itemsPath} onChange={(e) => set({ itemsPath: e.target.value })} />
        </Field>
      )}
      {kind === "transform" && <ProtectedField values={st.protectedCols} onChange={(protectedCols) => set({ protectedCols })} />}
      {kind !== "watch" && (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-[160px_minmax(0,1fr)]">
          <Field label="Time limit" htmlFor="ch-timeout" hint="ms">
            <Input id="ch-timeout" type="number" min={1} className="w-28" value={st.timeoutMS} onChange={(e) => set({ timeoutMS: e.target.value })} />
          </Field>
          <FailureChoice kind={kind} value={st.onFailure} onChange={(onFailure) => set({ onFailure })} protecting={st.protectedCols.length > 0} />
        </div>
      )}
      <KindNote kind={kind} />
    </>
  );
}

function ProtectedField({ values, onChange }: { values: string[]; onChange: (v: string[]) => void }) {
  return (
    <div className="space-y-2">
      <p className="text-sm font-medium">Protected columns and paths</p>
      <ListField label="Add a protected column" placeholder="catalog.schema.table.column, or body.card.number" values={values} onChange={onChange} />
      <p className="text-xs text-muted-foreground">
        A covered caller may select these as they are. Using one anywhere else (an expression, a filter, a join,
        grouping, ordering) is refused, naming the column and this handler. A statement the platform cannot read is
        refused too.
      </p>
    </div>
  );
}

const KIND_NOTES: Partial<Record<CallHandlerKind, string>> = {
  transform:
    "A transform reads with administrator authority. What it adds can show people data their persona cannot read: that is what it is for, and why only an administrator adds one.",
  watch:
    "After a matched call succeeds, one run is queued; the call is never delayed. A failed call queues nothing. A run holds the caller's arguments and part of the result: its inputs, prints and recordings are administrators' only.",
};

function KindNote({ kind }: { kind: CallHandlerKind }) {
  const note = KIND_NOTES[kind];
  if (!note) return null;
  return <p className="rounded-md bg-blue-50 p-2 text-xs text-blue-800 dark:bg-blue-500/10 dark:text-blue-300">{note}</p>;
}

// VersionOffer says which version of the script the handler runs and offers
// to approve the latest as the administrator.
function VersionOffer({
  kind,
  approval,
  approve,
  onApprove,
  adding,
}: {
  kind: CallHandlerKind;
  approval: { latest: number; running: number };
  approve: boolean;
  onApprove: (v: boolean) => void;
  adding: boolean;
}) {
  const waiting = approval.latest > approval.running;
  return (
    <div className="space-y-1 text-sm">
      <p className={waiting ? "text-amber-700 dark:text-amber-300" : "text-muted-foreground"}>
        {approval.running === 0
          ? `Version ${approval.latest} has not been approved.`
          : waiting
            ? `Version ${approval.running} runs; version ${approval.latest} waits for approval.`
            : `Version ${approval.running} is approved.`}{" "}
        It defines {KIND_WORDS[kind].entry}.
      </p>
      {(waiting || adding) && (
        <label className="flex items-start gap-2">
          <input type="checkbox" checked={approve} onChange={(e) => onApprove(e.target.checked)} />
          <span>Approve version {approval.latest} for handlers as you. It will run with your roles.</span>
        </label>
      )}
    </div>
  );
}

// FailureChoice is what happens when the script fails or runs out of time.
function FailureChoice({
  kind,
  value,
  onChange,
  protecting,
}: {
  kind: CallHandlerKind;
  value: string;
  onChange: (v: string) => void;
  protecting: boolean;
}) {
  const options =
    kind === "policy"
      ? [
          { value: "hold", label: "Hold the call for an administrator's approval" },
          { value: "deny", label: "Refuse the call" },
        ]
      : [
          { value: "fail", label: "Fail the call, naming this handler" },
          { value: "original", label: "Return the original result (not with protected columns)", disabled: protecting },
        ];
  return (
    <fieldset className="space-y-1">
      <legend className="mb-1 text-sm font-medium">If the script fails or runs out of time</legend>
      {options.map((o) => (
        <label key={o.value} className="flex items-start gap-2 text-sm">
          <input type="radio" name="ch-fail" checked={value === o.value} disabled={o.disabled} onChange={() => onChange(o.value)} />
          <span>{o.label}</span>
        </label>
      ))}
    </fieldset>
  );
}

// Conditions limit a handler to calls whose path parameter, body field or
// query parameter equals a value.
function Conditions({ conditions, onChange }: { conditions: CallCondition[]; onChange: (c: CallCondition[]) => void }) {
  const set = (i: number, c: Partial<CallCondition>) => onChange(conditions.map((x, j) => (j === i ? { ...x, ...c } : x)));
  return (
    <div className="space-y-2">
      <p className="text-sm font-medium">
        Only when <span className="font-normal text-muted-foreground">(optional)</span>
      </p>
      {conditions.map((c, i) => (
        <div key={i} className="grid grid-cols-[120px_minmax(0,1fr)_16px_minmax(0,1fr)_32px] items-center gap-2" data-testid="condition">
          <select
            aria-label="Where"
            className="h-8 rounded-md border bg-background px-2 text-sm"
            value={c.in}
            onChange={(e) => set(i, { in: e.target.value as CallCondition["in"] })}
          >
            <option value="path">path parameter</option>
            <option value="body">body field</option>
            <option value="query">query parameter</option>
          </select>
          <Input aria-label="Name" className="h-8" value={c.name} onChange={(e) => set(i, { name: e.target.value })} />
          <span className="text-center text-muted-foreground">=</span>
          <Input aria-label="Value" className="h-8" value={c.equals} onChange={(e) => set(i, { equals: e.target.value })} />
          <Button
            variant="ghost"
            size="icon"
            className="size-8"
            aria-label="Remove condition"
            onClick={() => onChange(conditions.filter((_, j) => j !== i))}
          >
            <X aria-hidden className="size-3.5" />
          </Button>
        </div>
      ))}
      <Button variant="link" size="sm" className="h-auto px-0" onClick={() => onChange([...conditions, { in: "path", name: "", equals: "" }])}>
        <Plus aria-hidden className="size-3.5" />
        Add a condition
      </Button>
    </div>
  );
}

function Field({
  label,
  htmlFor,
  hint,
  small,
  children,
}: {
  label: string;
  htmlFor: string;
  hint?: string;
  small?: boolean;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <Label htmlFor={htmlFor} className={small ? "text-xs text-muted-foreground" : "text-sm"}>
        {label}
      </Label>
      {children}
      {hint && <p className="text-xs text-muted-foreground">{hint}</p>}
    </div>
  );
}

// argsText is a handler's arguments as the form edits them.
function argsText(args?: Record<string, unknown> | null): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(args ?? {})) {
    out[k] = typeof v === "string" ? v : JSON.stringify(v);
  }
  return out;
}

// coercers read a parameter's text as its type.
const coercers: Record<string, (raw: string) => unknown> = {
  int: Number,
  integer: Number,
  float: Number,
  number: Number,
  bool: (raw) => raw === "true",
  boolean: (raw) => raw === "true",
  list: (raw) => {
    try {
      return JSON.parse(raw) as unknown;
    } catch {
      return raw.split(",").map((x) => x.trim()).filter(Boolean);
    }
  },
};

// typedArgs reads the form's text back as each parameter's type: a number, a
// boolean, a list, or text.
export function typedArgs(args: Record<string, string>, params: ScriptParam[]): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  for (const [k, raw] of Object.entries(args)) {
    if (raw === "") continue;
    const coerce = coercers[params.find((x) => x.name === k)?.type ?? ""];
    out[k] = coerce ? coerce(raw) : raw;
  }
  return out;
}
