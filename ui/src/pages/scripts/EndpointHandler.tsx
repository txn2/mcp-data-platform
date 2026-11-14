import { useState } from "react";
import { EndpointIdScope, useEndpointIds } from "./endpoints/endpointIds";
import { useSetScriptEndpoint, useSetScriptEndpointEnabled } from "@/api/portal/hooks/scriptEndpoint";
import type {
  EndpointAuthMode,
  EndpointFailure,
  ScriptEndpoint,
  ScriptEndpointInput,
} from "@/api/portal/hooks/scriptEndpoint";
import { useBotChecks } from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { ACCESS_LABELS, PUBLIC_NOTE } from "./endpoints/endpointLabels";
import { FormEmbed } from "./endpoints/FormEmbed";
import { PageFields, type PageLists, pageListInput, pageListsOf } from "./endpoints/PageFields";
import { EndpointFacts, withDefaults } from "./endpoints/EndpointFacts";
import { FormPublish, approvedVersion } from "./endpoints/FormPublish";
import { useApprovals } from "@/api/portal/hooks/approvals";
import { FormStorageFields } from "./endpoints/FormStorageFields";
import { formatWhen } from "./runFormat";
import { byName } from "@/lib/sortByName";

// EndpointHandler is one of the script's endpoint handlers (#2171, #2441): the
// HTTP route at /endpoints/{name} that runs its handle(req) on each request,
// its limits, and the requests that failed in the last seven days. Its owner
// reads it; an administrator adds an endpoint to any script, changes one, or
// turns it on and off, because an endpoint can answer the public. A form's
// owner does the same for their own form (#2494): for signed-in people at
// once, and for everyone once an administrator approved a version. Each
// endpoint has one access mode (#2259): authenticated callers it names,
// anyone, or anyone who passes a named bot check.

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE"];

const AUTH_MODES = Object.keys(ACCESS_LABELS) as EndpointAuthMode[];

// EndpointHandler is an endpoint handler, the controls over it, and the
// failed requests.
export function EndpointHandler({ scriptId, endpoint }: { scriptId: string; endpoint: ScriptEndpoint }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const isForm = Boolean(endpoint.is_form);
  // The section is shown to the script's owner and administrators only, so a
  // reader who is not an administrator is the owner (#2494).
  const canEdit = isAdmin || isForm;
  const [editing, setEditing] = useState(false);
  // warning is what the last save said beside its outcome (#2556), shown
  // until the next edit.
  const [warning, setWarning] = useState("");
  if (canEdit && editing) {
    const done = (saved?: ScriptEndpoint) => {
      setWarning(saved?.warning ?? "");
      setEditing(false);
    };
    return <EndpointEditor scriptId={scriptId} endpoint={endpoint} onDone={done} />;
  }
  return (
    <div className="space-y-4">
      <SaveWarning warning={warning} />
      <EndpointFacts endpoint={endpoint} />
      <BoundFormEmbed endpoint={endpoint} />
      {isForm && <FormPublish scriptId={scriptId} />}
      {canEdit && endpoint.handler && (
        <EndpointActions handlerId={endpoint.handler} endpoint={endpoint} onEdit={() => setEditing(true)} />
      )}
      <FailureList failures={endpoint.failures ?? []} />
    </div>
  );
}

// SaveWarning is what an endpoint's save said beside its outcome (#2556),
// nothing when it said nothing more.
export function SaveWarning({ warning }: { warning?: string }) {
  if (!warning) return null;
  return (
    <Alert variant="warning" role="status" data-testid="endpoint-save-warning">
      <AlertDescription>
        <span className="font-medium">Saved.</span> {warning}
      </AlertDescription>
    </Alert>
  );
}

// EndpointEditor is the form that adds an endpoint handler, or changes the
// one endpoint.handler names, offering the access modes the caller may
// choose.
export function EndpointEditor({
  scriptId,
  endpoint,
  onDone,
}: {
  scriptId: string;
  endpoint: ScriptEndpoint;
  onDone: (saved?: ScriptEndpoint) => void;
}) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const approvals = useApprovals({ scriptId, status: "approved" });
  const modes = modesFor(isAdmin, approvedVersion(approvals.data));
  // Scoped ids: an endpoint being changed and one being added can be open at
  // once (#2441).
  return (
    <EndpointIdScope prefix={endpoint.handler ? `ep-${endpoint.handler}` : "ep-new"}>
      <EndpointForm scriptId={scriptId} endpoint={endpoint} modes={modes} onDone={onDone} />
    </EndpointIdScope>
  );
}

// The access modes a form's owner chooses from: signed-in people at once,
// and everyone once a version is approved (#2494). A bot check is chosen by
// an administrator, who reads the deployment's checks.
const internalModes: EndpointAuthMode[] = ["authenticated"];
const publicModes: EndpointAuthMode[] = ["authenticated", "public"];

function modesFor(isAdmin: boolean, approved: number): EndpointAuthMode[] {
  if (isAdmin) return AUTH_MODES;
  return approved > 0 ? publicModes : internalModes;
}

// BoundFormEmbed is a bound form's embed snippet (#2331), nothing for any
// other endpoint.
function BoundFormEmbed({ endpoint }: { endpoint: ScriptEndpoint }) {
  if (!endpoint.bound || !endpoint.page_url) return null;
  return (
    <FormEmbed pageUrl={endpoint.page_url} name={endpoint.endpoint ?? endpoint.name} allowedOrigins={endpoint.allowed_origins ?? []} />
  );
}

function EndpointActions({ handlerId, endpoint, onEdit }: { handlerId: string; endpoint: ScriptEndpoint; onEdit: () => void }) {
  const setEnabled = useSetScriptEndpointEnabled(handlerId);
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={onEdit}>
          Edit
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={setEnabled.isPending}
          onClick={() => setEnabled.mutate(!endpoint.enabled)}
        >
          {endpoint.enabled ? "Turn off" : "Turn on"}
        </Button>
      </div>
      {setEnabled.error && (
        <Alert variant="destructive">
          <AlertDescription>{setEnabled.error.message}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}

// formState is the form's fields as strings, read back into a binding on save.
interface formState {
  endpoint: string;
  methods: string[];
  path: string;
  authMode: EndpointAuthMode;
  // callers is one entry a line; everyCaller stands for "*".
  callers: string;
  everyCaller: boolean;
  botCheck: string;
  checkedMethods: string[];
  timeout: string;
  memoryMiB: string;
  bodyKiB: string;
  rateLimit: string;
  callerRateLimit: string;
  // ipRateLimit is empty for no per-address limit (#2556).
  ipRateLimit: string;
  origins: string;
  responseMode: "api" | "page";
  pageLists: PageLists;
  // connection and decryptPersonas are a form's (#2178, #2179).
  connection: string;
  decryptPersonas: string[];
}

function initialForm(endpoint: ScriptEndpoint): formState {
  const e = withDefaults(endpoint);
  return {
    endpoint: e.endpoint,
    methods: e.methods,
    path: e.path,
    authMode: e.auth_mode,
    callers: e.callers.filter((c) => c !== "*").join("\n"),
    everyCaller: e.callers.includes("*"),
    botCheck: e.bot_check,
    checkedMethods: e.checked_methods,
    timeout: String(e.timeout_seconds),
    memoryMiB: String(Math.round(e.max_memory_bytes / (1 << 20))),
    bodyKiB: String(Math.round(e.max_body_bytes / (1 << 10))),
    rateLimit: String(e.rate_limit),
    callerRateLimit: String(e.caller_rate_limit),
    ipRateLimit: e.ip_rate_limit > 0 ? String(e.ip_rate_limit) : "",
    origins: e.allowed_origins.join("\n"),
    responseMode: e.response_mode,
    pageLists: pageListsOf(e),
    connection: e.form?.connection ?? (e.form_connections?.length === 1 ? (e.form_connections[0] ?? "") : ""),
    decryptPersonas: e.form?.decrypt_personas ?? [],
  };
}

function toInput(f: formState, endpoint: ScriptEndpoint): ScriptEndpointInput {
  return {
    ...formFields(f, endpoint),
    endpoint: f.endpoint.trim(),
    methods: f.methods,
    path: f.path.trim(),
    auth_mode: f.authMode,
    ...modeFields(f),
    timeout_seconds: Number(f.timeout),
    max_memory_bytes: Number(f.memoryMiB) * (1 << 20),
    max_body_bytes: Number(f.bodyKiB) * (1 << 10),
    rate_limit: Number(f.rateLimit),
    caller_rate_limit: Number(f.callerRateLimit),
    ip_rate_limit: f.ipRateLimit.trim() ? Number(f.ipRateLimit) : 0,
    allowed_origins: f.origins.split("\n").map((o) => o.trim()).filter(Boolean),
    response_mode: f.responseMode,
    ...pageListInput(f.responseMode, f.pageLists),
  };
}

// formIncomplete reports whether the form lacks a field a save needs: a
// name, a method, a bot_check endpoint's check, and a new form's connection.
function formIncomplete(form: formState, endpoint: ScriptEndpoint): boolean {
  if (!form.endpoint.trim() || form.methods.length === 0) return true;
  if (form.authMode === "bot_check" && !form.botCheck) return true;
  return Boolean(endpoint.is_form) && !endpoint.form && !form.connection;
}

// formFields are a form's storage fields (#2178): the connection its source
// is made on, sent until the source exists, and the decrypt personas.
function formFields(f: formState, endpoint: ScriptEndpoint): ScriptEndpointInput {
  if (!endpoint.is_form) return {};
  return {
    ...(endpoint.form ? {} : { connection: f.connection }),
    decrypt_personas: f.decryptPersonas,
  };
}

// modeFields are the fields of the chosen access mode alone; the server
// clears the others when the mode changes and refuses them on another mode.
function modeFields(f: formState): ScriptEndpointInput {
  switch (f.authMode) {
    case "authenticated":
      return {
        callers: f.everyCaller
          ? ["*"]
          : f.callers
              .split("\n")
              .map((c) => c.trim())
              .filter(Boolean),
      };
    case "bot_check":
      return { bot_check: f.botCheck, checked_methods: f.checkedMethods.filter((m) => f.methods.includes(m)) };
    default:
      return {};
  }
}

function EndpointForm({
  scriptId,
  endpoint,
  modes,
  onDone,
}: {
  scriptId: string;
  endpoint: ScriptEndpoint;
  modes: EndpointAuthMode[];
  onDone: (saved?: ScriptEndpoint) => void;
}) {
  const eid = useEndpointIds();
  const save = useSetScriptEndpoint(scriptId, endpoint.handler);
  const [form, setForm] = useState<formState>(() => initialForm(endpoint));
  const set = <K extends keyof formState>(key: K, value: formState[K]) => setForm((f) => ({ ...f, [key]: value }));
  const toggleMethod = (m: string) =>
    set("methods", form.methods.includes(m) ? form.methods.filter((x) => x !== m) : [...form.methods, m]);
  const toggleChecked = (m: string) =>
    set(
      "checkedMethods",
      form.checkedMethods.includes(m) ? form.checkedMethods.filter((x) => x !== m) : [...form.checkedMethods, m],
    );
  const submit = () => save.mutate(toInput(form, endpoint), { onSuccess: (saved) => onDone(saved) });
  const incomplete = formIncomplete(form, endpoint);
  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id={eid("endpoint-name")} label="Name">
          <Input id={eid("endpoint-name")} value={form.endpoint} placeholder="order-lookup" onChange={(e) => set("endpoint", e.target.value)} />
        </Field>
        <Field id={eid("endpoint-path")} label="Path">
          <Input id={eid("endpoint-path")} value={form.path} placeholder="/{id}" onChange={(e) => set("path", e.target.value)} />
          <p className="mt-1 text-xs text-muted-foreground">A last segment written {"{name...}"} matches the rest of the path, at any depth. The handler reads it as req["params"]["name"].</p>
        </Field>
        <Field id={eid("endpoint-access")} label="Access">
          <select
            id={eid("endpoint-access")}
            className="h-9 w-full rounded-md border bg-background px-2 text-sm"
            value={form.authMode}
            onChange={(e) => set("authMode", e.target.value as EndpointAuthMode)}
          >
            {(modes.includes(form.authMode) ? modes : [...modes, form.authMode]).map((m) => (
              <option key={m} value={m}>
                {ACCESS_LABELS[m]}
              </option>
            ))}
          </select>
        </Field>
        <fieldset className="space-y-1">
          <legend className="text-sm font-medium">Methods</legend>
          <div className="flex flex-wrap gap-3 pt-1">
            {METHODS.map((m) => (
              <label key={m} className="flex items-center gap-1 text-sm">
                <input type="checkbox" checked={form.methods.includes(m)} onChange={() => toggleMethod(m)} />
                {m}
              </label>
            ))}
          </div>
        </fieldset>
        <NumberField id={eid("endpoint-timeout")} label="Timeout (seconds)" value={form.timeout} onChange={(v) => set("timeout", v)} />
        <NumberField id={eid("endpoint-memory")} label="Memory (MiB)" value={form.memoryMiB} onChange={(v) => set("memoryMiB", v)} />
        <NumberField id={eid("endpoint-body")} label="Body limit (KiB)" value={form.bodyKiB} onChange={(v) => set("bodyKiB", v)} />
        <NumberField id={eid("endpoint-rate")} label="Rate limit (a minute)" value={form.rateLimit} onChange={(v) => set("rateLimit", v)} />
        {form.authMode === "authenticated" && (
          <NumberField
            id={eid("endpoint-caller-rate")}
            label="Per-caller limit (a minute)"
            value={form.callerRateLimit}
            onChange={(v) => set("callerRateLimit", v)}
          />
        )}
        <Field id={eid("endpoint-ip-rate")} label="Per-address limit (a minute)">
          <Input id={eid("endpoint-ip-rate")} type="number" min={1} value={form.ipRateLimit} placeholder="No limit" onChange={(e) => set("ipRateLimit", e.target.value)} />
          <p className="mt-1 text-xs text-muted-foreground">Requests from one visitor's address, on each replica. An IPv6 visitor is counted by their /64. Empty: no limit.</p>
        </Field>
      </div>
      <ModeFields form={form} set={set} onToggle={toggleChecked} />
      {endpoint.is_form && (
        <FormStorageFields
          endpoint={endpoint}
          connection={form.connection}
          decryptPersonas={form.decryptPersonas}
          onConnection={(v) => set("connection", v)}
          onDecryptPersonas={(v) => set("decryptPersonas", v)}
        />
      )}
      <PageFields
        endpoint={endpoint}
        responseMode={form.responseMode}
        lists={form.pageLists}
        onMode={(v) => set("responseMode", v)}
        onList={(key, v) => set("pageLists", { ...form.pageLists, [key]: v })}
      />
      <Field id={eid("endpoint-origins")} label="Allowed origins">
        <Textarea
          id={eid("endpoint-origins")}
          rows={3}
          value={form.origins}
          placeholder="https://forms.example.com"
          onChange={(e) => set("origins", e.target.value)}
        />
      </Field>
      {save.error && (
        <Alert variant="destructive">
          <AlertDescription>{save.error.message}</AlertDescription>
        </Alert>
      )}
      <div className="flex gap-2">
        <Button
          size="sm"
          disabled={save.isPending || incomplete}
          onClick={submit}
        >
          {save.isPending ? "Saving..." : "Save"}
        </Button>
        <Button size="sm" variant="ghost" disabled={save.isPending} onClick={() => onDone()}>
          Cancel
        </Button>
      </div>
    </div>
  );
}

type setField = <K extends keyof formState>(key: K, value: formState[K]) => void;

// ModeFields are the fields of the chosen access mode.
function ModeFields({ form, set, onToggle }: { form: formState; set: setField; onToggle: (m: string) => void }) {
  switch (form.authMode) {
    case "authenticated":
      return <CallersFields form={form} set={set} />;
    case "bot_check":
      return <BotCheckFields form={form} set={set} onToggle={onToggle} />;
    default:
      return (
        <p className="text-sm text-muted-foreground">
          {PUBLIC_NOTE} An automated caller, a partner system included, is better given an authenticated endpoint and an
          API key.
        </p>
      );
  }
}

// CallersFields are who an authenticated endpoint admits.
function CallersFields({ form, set }: { form: formState; set: setField }) {
  const eid = useEndpointIds();
  return (
    <div className="space-y-2">
      <label className="flex items-center gap-2 text-sm">
        <input type="checkbox" checked={form.everyCaller} onChange={(e) => set("everyCaller", e.target.checked)} />
        Every authenticated caller
      </label>
      {!form.everyCaller && (
        <Field id={eid("endpoint-callers")} label="Callers">
          <Textarea
            id={eid("endpoint-callers")}
            rows={3}
            value={form.callers}
            placeholder={"persona:analyst\napi_key:partner-sync"}
            onChange={(e) => set("callers", e.target.value)}
          />
          <p className="text-xs text-muted-foreground">
            One a line: persona:&lt;name&gt; admits the callers of that persona, api_key:&lt;name&gt; that API key. An
            empty list admits no one but the administrator, who is always admitted.
          </p>
        </Field>
      )}
    </div>
  );
}

// BotCheckFields are the check a bot_check endpoint names and the methods it
// checks.
function BotCheckFields({
  form,
  set,
  onToggle,
}: {
  form: formState;
  set: setField;
  onToggle: (m: string) => void;
}) {
  const eid = useEndpointIds();
  const { data } = useBotChecks();
  const checks = [...(data?.checks ?? [])].sort(byName((c) => c.name));
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <Field id={eid("endpoint-bot-check")} label="Bot check">
        <select
          id={eid("endpoint-bot-check")}
          className="h-9 w-full rounded-md border bg-background px-2 text-sm"
          value={form.botCheck}
          onChange={(e) => set("botCheck", e.target.value)}
        >
          <option value="">Choose a bot check</option>
          {checks.map((c) => (
            <option key={c.name} value={c.name} disabled={!c.usable}>
              {c.name}
            </option>
          ))}
          {form.botCheck && !checks.some((c) => c.name === form.botCheck) && (
            <option value={form.botCheck}>{form.botCheck}</option>
          )}
        </select>
        <p className="text-xs text-muted-foreground">Bot checks are configured under Admin &gt; Settings &gt; Bot checks.</p>
      </Field>
      <fieldset className="space-y-1">
        <legend className="text-sm font-medium">Checked methods</legend>
        <div className="flex flex-wrap gap-3 pt-1">
          {form.methods.map((m) => (
            <label key={m} className="flex items-center gap-1 text-sm">
              <input type="checkbox" checked={form.checkedMethods.includes(m)} onChange={() => onToggle(m)} />
              {m}
            </label>
          ))}
        </div>
        <p className="text-xs text-muted-foreground">
          None ticked checks every method. Tick POST alone to serve a form&apos;s page on GET without a check.
        </p>
      </fieldset>
    </div>
  );
}

function Field({ id, label, children }: { id: string; label: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>{label}</Label>
      {children}
    </div>
  );
}

function NumberField({ id, label, value, onChange }: { id: string; label: string; value: string; onChange: (v: string) => void }) {
  return (
    <Field id={id} label={label}>
      <Input id={id} type="number" min={1} value={value} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

function FailureList({ failures }: { failures: EndpointFailure[] }) {
  return (
    <div className="space-y-2">
      <h3 className="text-sm font-medium">Failed requests</h3>
      {failures.length === 0 ? (
        <p className="text-sm text-muted-foreground">No request failed in the last seven days.</p>
      ) : (
        <div className="overflow-x-auto rounded-md border">
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left text-muted-foreground">
              <tr>
                <th className="px-3 py-2 font-medium">Time</th>
                <th className="px-3 py-2 font-medium">Method</th>
                <th className="px-3 py-2 font-medium">Status</th>
                <th className="px-3 py-2 font-medium">Error</th>
                <th className="px-3 py-2 font-medium">Duration</th>
              </tr>
            </thead>
            <tbody className="divide-y">
              {failures.map((f) => (
                <tr key={f.id} className="align-top">
                  <td className="whitespace-nowrap px-3 py-2">{formatWhen(f.created_at)}</td>
                  <td className="px-3 py-2">{f.method}</td>
                  <td className="px-3 py-2">{f.status}</td>
                  <td className="px-3 py-2">
                    <pre className="max-h-32 max-w-xl overflow-auto whitespace-pre-wrap break-words font-mono text-xs">{f.error}</pre>
                  </td>
                  <td className="whitespace-nowrap px-3 py-2">{f.duration_ms} ms</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </div>
  );
}
