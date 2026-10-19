import { useState } from "react";
import {
  useScriptEndpoint,
  useSetScriptEndpoint,
  useSetScriptEndpointEnabled,
} from "@/api/portal/hooks/scriptEndpoint";
import type {
  EndpointAuthMode,
  EndpointFailure,
  ScriptEndpoint,
  ScriptEndpointInput,
} from "@/api/portal/hooks/scriptEndpoint";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { useAuthStore } from "@/stores/auth";
import { formatBytes, formatWhen } from "./runFormat";

// ScriptEndpointCard is the script's endpoint (#2171): the HTTP route at
// /endpoints/{name} that runs its handle(req) on each request, the binding's
// limits, and the requests that failed in the last seven days. Its owner reads
// it; only an administrator binds the script, changes the binding, or turns
// it on and off, because an endpoint can answer the public.

const METHODS = ["GET", "POST", "PUT", "PATCH", "DELETE"];

const AUTH_LABELS: Record<EndpointAuthMode, string> = {
  signed_in: "Signed in",
  public: "Public",
};

export function ScriptEndpointCard({ scriptId }: { scriptId: string }) {
  const { data, isLoading, error } = useScriptEndpoint(scriptId, true);
  // A deployment that cannot bind scripts answers null: nothing to show.
  if (data === null) return null;
  return (
    <SectionCard title="Endpoint" collapsible defaultOpen={false} summary={endpointSummary(data)}>
      <div className="space-y-4">
        {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>The endpoint could not be read.</AlertDescription>
          </Alert>
        )}
        {data && <EndpointBody scriptId={scriptId} endpoint={data} />}
      </div>
    </SectionCard>
  );
}

// EndpointBody is the binding, the administrator's controls over it, and
// the failed requests.
function EndpointBody({ scriptId, endpoint }: { scriptId: string; endpoint: ScriptEndpoint }) {
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [editing, setEditing] = useState(false);
  if (isAdmin && editing) {
    return <EndpointForm scriptId={scriptId} endpoint={endpoint} onDone={() => setEditing(false)} />;
  }
  return (
    <>
      {endpoint.bound ? <EndpointFacts endpoint={endpoint} /> : <Unbound isAdmin={isAdmin} />}
      {isAdmin && <EndpointActions scriptId={scriptId} endpoint={endpoint} onEdit={() => setEditing(true)} />}
      {endpoint.bound && <FailureList failures={endpoint.failures ?? []} />}
    </>
  );
}

function Unbound({ isAdmin }: { isAdmin: boolean }) {
  return (
    <p className="text-sm text-muted-foreground">
      {isAdmin
        ? "This script answers no HTTP requests. Bind it to an endpoint to run handle(req) on each request to /endpoints/{name}."
        : "Only an administrator can bind a script to an endpoint."}
    </p>
  );
}

function endpointSummary(e?: ScriptEndpoint): string | undefined {
  if (!e) return undefined;
  if (!e.bound) return "Not bound";
  return `${e.enabled ? "On" : "Off"} at /endpoints/${e.endpoint}`;
}

// DEFAULTS are a binding's values where the platform reports none: a new
// binding's defaults, which epbinding.Build applies.
const DEFAULTS = {
  endpoint: "",
  url: "",
  methods: ["GET"],
  path: "",
  auth_mode: "signed_in" as EndpointAuthMode,
  open_reads: false,
  enabled: true,
  timeout_seconds: 10,
  max_memory_bytes: 64 << 20,
  max_body_bytes: 1 << 20,
  allowed_origins: [] as string[],
  rate_limit: 600,
  caller_rate_limit: 120,
};

// withDefaults is e with every field the binding has filled.
function withDefaults(e: ScriptEndpoint): typeof DEFAULTS & ScriptEndpoint {
  const defined = Object.fromEntries(Object.entries(e).filter(([, v]) => v !== undefined));
  return { ...DEFAULTS, ...defined } as typeof DEFAULTS & ScriptEndpoint;
}

// factsOf is the binding as rows of label and value.
function factsOf(endpoint: ScriptEndpoint): [string, string][] {
  const e = withDefaults(endpoint);
  const perReplica = (n: number) => `${n} a minute on each replica`;
  const rows: [string, string][] = [
    ["Address", e.url],
    ["Methods", e.methods.join(", ")],
    ["Path", e.path || "—"],
    ["Callers", AUTH_LABELS[e.auth_mode]],
  ];
  if (e.auth_mode === "public") rows.push(["Reads without a check", e.open_reads ? "Yes" : "No"]);
  rows.push(
    ["Timeout", `${e.timeout_seconds} s`],
    ["Memory", formatBytes(e.max_memory_bytes)],
    ["Body limit", formatBytes(e.max_body_bytes)],
    ["Rate limit", perReplica(e.rate_limit)],
  );
  if (e.auth_mode === "signed_in") rows.push(["Per-caller limit", perReplica(e.caller_rate_limit)]);
  rows.push(["Allowed origins", e.allowed_origins.join(", ") || "None"]);
  return rows;
}

function EndpointFacts({ endpoint }: { endpoint: ScriptEndpoint }) {
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-2 text-sm sm:grid-cols-[10rem_1fr]">
      <dt className="text-muted-foreground">Status</dt>
      <dd>
        <Badge variant={endpoint.enabled ? "success" : "muted"}>{endpoint.enabled ? "On" : "Off"}</Badge>
      </dd>
      {factsOf(endpoint).map(([label, value]) => (
        <div key={label} className="contents">
          <dt className="text-muted-foreground">{label}</dt>
          <dd className="min-w-0 break-all font-medium">{value}</dd>
        </div>
      ))}
      {endpoint.updated_by && (
        <>
          <dt className="text-muted-foreground">Changed</dt>
          <dd className="text-muted-foreground">
            {formatWhen(endpoint.updated_at)} by {endpoint.updated_by}
          </dd>
        </>
      )}
    </dl>
  );
}

function EndpointActions({ scriptId, endpoint, onEdit }: { scriptId: string; endpoint: ScriptEndpoint; onEdit: () => void }) {
  const setEnabled = useSetScriptEndpointEnabled(scriptId);
  return (
    <div className="space-y-2">
      <div className="flex flex-wrap gap-2">
        <Button size="sm" onClick={onEdit}>
          {endpoint.bound ? "Edit" : "Bind"}
        </Button>
        {endpoint.bound && (
          <Button
            size="sm"
            variant="outline"
            disabled={setEnabled.isPending}
            onClick={() => setEnabled.mutate(!endpoint.enabled)}
          >
            {endpoint.enabled ? "Turn off" : "Turn on"}
          </Button>
        )}
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
  openReads: boolean;
  timeout: string;
  memoryMiB: string;
  bodyKiB: string;
  rateLimit: string;
  callerRateLimit: string;
  origins: string;
}

function initialForm(endpoint: ScriptEndpoint): formState {
  const e = withDefaults(endpoint);
  return {
    endpoint: e.endpoint,
    methods: e.methods,
    path: e.path,
    authMode: e.auth_mode,
    openReads: e.open_reads,
    timeout: String(e.timeout_seconds),
    memoryMiB: String(Math.round(e.max_memory_bytes / (1 << 20))),
    bodyKiB: String(Math.round(e.max_body_bytes / (1 << 10))),
    rateLimit: String(e.rate_limit),
    callerRateLimit: String(e.caller_rate_limit),
    origins: e.allowed_origins.join("\n"),
  };
}

function toInput(f: formState): ScriptEndpointInput {
  return {
    endpoint: f.endpoint.trim(),
    methods: f.methods,
    path: f.path.trim(),
    auth_mode: f.authMode,
    open_reads: f.authMode === "public" && f.openReads,
    timeout_seconds: Number(f.timeout),
    max_memory_bytes: Number(f.memoryMiB) * (1 << 20),
    max_body_bytes: Number(f.bodyKiB) * (1 << 10),
    rate_limit: Number(f.rateLimit),
    caller_rate_limit: Number(f.callerRateLimit),
    allowed_origins: f.origins.split("\n").map((o) => o.trim()).filter(Boolean),
  };
}

function EndpointForm({ scriptId, endpoint, onDone }: { scriptId: string; endpoint: ScriptEndpoint; onDone: () => void }) {
  const save = useSetScriptEndpoint(scriptId);
  const [form, setForm] = useState<formState>(() => initialForm(endpoint));
  const set = <K extends keyof formState>(key: K, value: formState[K]) => setForm((f) => ({ ...f, [key]: value }));
  const toggleMethod = (m: string) =>
    set("methods", form.methods.includes(m) ? form.methods.filter((x) => x !== m) : [...form.methods, m]);
  const submit = () => save.mutate(toInput(form), { onSuccess: onDone });
  return (
    <div className="space-y-4">
      <div className="grid gap-4 sm:grid-cols-2">
        <Field id="endpoint-name" label="Name">
          <Input id="endpoint-name" value={form.endpoint} placeholder="order-lookup" onChange={(e) => set("endpoint", e.target.value)} />
        </Field>
        <Field id="endpoint-path" label="Path">
          <Input id="endpoint-path" value={form.path} placeholder="/{id}" onChange={(e) => set("path", e.target.value)} />
        </Field>
        <Field id="endpoint-callers" label="Callers">
          <select
            id="endpoint-callers"
            className="h-9 w-full rounded-md border bg-background px-2 text-sm"
            value={form.authMode}
            onChange={(e) => set("authMode", e.target.value as EndpointAuthMode)}
          >
            {(Object.keys(AUTH_LABELS) as EndpointAuthMode[]).map((m) => (
              <option key={m} value={m}>
                {AUTH_LABELS[m]}
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
        <NumberField id="endpoint-timeout" label="Timeout (seconds)" value={form.timeout} onChange={(v) => set("timeout", v)} />
        <NumberField id="endpoint-memory" label="Memory (MiB)" value={form.memoryMiB} onChange={(v) => set("memoryMiB", v)} />
        <NumberField id="endpoint-body" label="Body limit (KiB)" value={form.bodyKiB} onChange={(v) => set("bodyKiB", v)} />
        <NumberField id="endpoint-rate" label="Rate limit (a minute)" value={form.rateLimit} onChange={(v) => set("rateLimit", v)} />
        {form.authMode === "signed_in" && (
          <NumberField
            id="endpoint-caller-rate"
            label="Per-caller limit (a minute)"
            value={form.callerRateLimit}
            onChange={(v) => set("callerRateLimit", v)}
          />
        )}
        {form.authMode === "public" && (
          <label className="flex items-center gap-2 self-end text-sm">
            <input type="checkbox" checked={form.openReads} onChange={(e) => set("openReads", e.target.checked)} />
            Reads without a check
          </label>
        )}
      </div>
      <Field id="endpoint-origins" label="Allowed origins">
        <Textarea
          id="endpoint-origins"
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
        <Button size="sm" disabled={save.isPending || !form.endpoint.trim() || form.methods.length === 0} onClick={submit}>
          {save.isPending ? "Saving..." : "Save"}
        </Button>
        <Button size="sm" variant="ghost" disabled={save.isPending} onClick={onDone}>
          Cancel
        </Button>
      </div>
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
