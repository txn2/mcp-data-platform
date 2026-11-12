import type { EndpointAuthMode, ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { Badge } from "@/components/ui/badge";
import { formatBytes, formatWhen } from "../runFormat";
import { PUBLIC_NOTE, accessLabel, callersSummary } from "./endpointLabels";
import { formStorage } from "./FormStorageFields";
import { pageFacts } from "./PageFields";

// EndpointFacts is a bound endpoint as the card reads it: each setting as a
// row of label and value (#2171), with the catch-all path, the per-address
// limit and a page's origin lists (#2553, #2554, #2556).

// DEFAULTS are a binding's values where the platform reports none: a new
// binding's defaults, which epbinding.Build applies.
const DEFAULTS = {
  endpoint: "",
  url: "",
  methods: ["GET"],
  path: "",
  auth_mode: "authenticated" as EndpointAuthMode,
  callers: [] as string[],
  bot_check: "",
  checked_methods: [] as string[],
  enabled: true,
  timeout_seconds: 10,
  max_memory_bytes: 64 << 20,
  max_body_bytes: 1 << 20,
  allowed_origins: [] as string[],
  response_mode: "api" as "api" | "page",
  page_cdns: [] as string[],
  rate_limit: 600,
  caller_rate_limit: 120,
  ip_rate_limit: 0,
};

// withDefaults is e with every field the binding has filled.
export function withDefaults(e: ScriptEndpoint): typeof DEFAULTS & ScriptEndpoint {
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
    ["Path", pathFact(e.path)],
    ["Access", accessLabel(e)],
  ];
  if (e.auth_mode === "authenticated") rows.push(["Admits", callersSummary(e.callers)]);
  if (e.auth_mode === "bot_check") {
    rows.push(["Checked methods", e.checked_methods.length === 0 ? "Every method" : e.checked_methods.join(", ")]);
  }
  if (e.auth_mode === "public") rows.push(["Check", PUBLIC_NOTE]);
  rows.push(
    ["Timeout", `${e.timeout_seconds} s`],
    ["Memory", formatBytes(e.max_memory_bytes)],
    ["Body limit", formatBytes(e.max_body_bytes)],
    ["Rate limit", perReplica(e.rate_limit)],
  );
  if (e.auth_mode === "authenticated") rows.push(["Per-caller limit", perReplica(e.caller_rate_limit)]);
  if (e.ip_rate_limit > 0) rows.push(["Per-address limit", `${e.ip_rate_limit} a minute for each address, on each replica`]);
  rows.push(...pageFacts(e));
  rows.push(["Allowed origins", e.allowed_origins.join(", ") || "None"]);
  if (e.form) {
    rows.push(
      ["Submissions", formStorage(e.form.table, e.form.connection)],
      ["Decrypt personas", e.form.decrypt_personas.join(", ") || "None"],
    );
  }
  return rows;
}



// CATCH_ALL is a path whose last segment takes the rest of the path (#2553).
const CATCH_ALL = /\{([a-z][a-z0-9_]*)\.{3}\}$/;

/** pathFact is the Path row: the template, and what a catch-all hands the handler. */
function pathFact(path: string): string {
  if (!path) return "—";
  const m = CATCH_ALL.exec(path);
  return m ? `${path} · every path under it, as ${m[1]}` : path;
}

export function EndpointFacts({ endpoint }: { endpoint: ScriptEndpoint }) {
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
