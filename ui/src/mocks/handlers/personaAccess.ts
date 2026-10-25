import { http, HttpResponse } from "msw";
import type { EffectiveAccess, PersonaCreateRequest, ToolDecision } from "@/api/admin/types";
import { matchPattern } from "@/pages/settings/persona/resolve";
import { FULL_PORTAL, resolvePortal } from "@/lib/portalAccess";
import type { Share } from "@/api/portal/types";
import { mockResources } from "../data/resources";

// What a persona reaches (#2257) and files shared with the caller (#2258), for
// the demo and the screenshots. The evaluation is the server's rule over a
// small fixed deployment: a Trino toolkit over a warehouse and a connection
// with a scratch catalog, an API connection, and the platform tools.

const ADMIN_BASE = "/api/v1/admin";

const TOOLS: { name: string; kind?: string }[] = [
  { name: "search" },
  { name: "fetch" },
  { name: "memory_manage" },
  { name: "trino_query", kind: "trino" },
  { name: "trino_execute", kind: "trino" },
  { name: "api_invoke_endpoint", kind: "api" },
];

const CONNECTIONS = [
  { name: "acme-trino", kind: "trino", scratch: { catalog: "scratch", schema: "uploads" } },
  { name: "warehouse", kind: "trino" },
  { name: "crm-api", kind: "api", channels: ["ops-alerts"] },
];

const OPERATIONS = [
  { connection: "crm-api", method: "GET", path: "/contacts" },
  { connection: "crm-api", method: "DELETE", path: "/contacts/{id}" },
];

function decide(allow: string[] = [], deny: string[] = [], name: string): { decision: ToolDecision; matched?: string } {
  const d = deny.find((p) => matchPattern(p, name));
  if (d) return { decision: "deny", matched: d };
  const a = allow.find((p) => matchPattern(p, name));
  if (a) return { decision: "allow", matched: a };
  return { decision: "none" };
}

function evaluate(p: PersonaCreateRequest): EffectiveAccess {
  const ext = !!p.external;
  const admin = p.name === "admin" && !ext;
  const conns = CONNECTIONS.map((c) => {
    const d = decide(p.allow_connections, p.deny_connections, c.name);
    return { name: c.name, kind: c.kind, decision: d.decision as "allow" | "deny" | "none", matched: d.matched, exact: d.matched === c.name, scratch: c.scratch, used_by_channels: c.channels ?? [] };
  });
  const allowed = (name: string) => conns.find((c) => c.name === name)?.decision === "allow";
  const tools = TOOLS.map((t) => {
    const d = decide(p.allow_tools, p.deny_tools, t.name);
    const reach = CONNECTIONS.filter((c) => t.kind && c.kind === t.kind).map((c) => ({ name: c.name, allowed: allowed(c.name) }));
    const callable = !ext && d.decision === "allow" && (reach.length === 0 || reach.some((c) => c.allowed));
    return { name: t.name, kind: t.kind, decision: ext ? ("external" as const) : d.decision, matched: d.matched, exact: d.matched === t.name, connections: reach, callable };
  });
  const callable = (n: string) => tools.find((t) => t.name === n)?.callable ?? false;
  const ops = OPERATIONS.map((o) => {
    const decision = ext ? "external" : !callable("api_invoke_endpoint") ? "tool" : !allowed(o.connection) ? "connection" : "open";
    return { ...o, decision: decision as EffectiveAccess["api_operations"]["items"][number]["decision"], rule_index: -1, callable: decision === "open" };
  });
  const stored = resolvePortal(p.portal);
  const effective = admin ? FULL_PORTAL : ext ? { ...stored, assets: "shared" as const, resources: stored.resources === "none" ? ("none" as const) : ("shared" as const), knowledge: "none" as const, catalog: "none" as const, apis: "none" as const, scratch_tables: "none" as const, search: false, prompts: false, automations: false, memory: false, external: true } : stored;
  const scratch = CONNECTIONS.filter((c) => c.scratch && allowed(c.name) && !ext).map((c) => ({
    connection: c.name, catalog: c.scratch!.catalog, schema: c.scratch!.schema,
    list: effective.scratch_tables !== "none", register: effective.scratch_tables === "register",
    drop_via_sql: decide(p.allow_tools, p.deny_tools, "trino_execute").decision === "allow", registrations: 14,
    queryable_from: callable("trino_query") ? CONNECTIONS.filter((x) => x.kind === "trino" && allowed(x.name)).map((x) => x.name) : [],
  }));
  return {
    is_admin: admin,
    admin_persona: "admin",
    account_type: ext ? "external" : p.service_account ? "automated" : "internal",
    calls_excluded_by_config: false,
    assignment: { roles: p.roles, role_mapping: [], folder_reader_roles: p.roles, folder_writer_role: `persona-admin:${p.name}`, role_overlaps: [] },
    tools: { total: tools.length, callable: tools.filter((t) => t.callable).length, items: tools },
    connections: { total: conns.length, allowed: conns.filter((c) => c.decision === "allow").length, items: conns },
    api_operations: { total: ops.length, callable: ops.filter((o) => o.callable).length, items: ops },
    scratch,
    portal: { stored, effective, capped_by_external: ext ? ["assets", "resources", "knowledge", "catalog", "apis", "scratch_tables", "search", "prompts", "automations", "memory"].filter((k) => JSON.stringify(stored[k as keyof typeof stored]) !== JSON.stringify(effective[k as keyof typeof effective])) : [] },
    sharing: admin || (!ext && p.allow_sharing !== false),
    resource_scopes: admin ? ["user", "persona:*", "global"] : ext ? ["user", `persona:${p.name}`] : ["user", `persona:${p.name}`, "global"],
    references: { secrets: { named: p.name === "data-engineer" ? ["crm-token"] : [], unrestricted: 1 }, script_grants: p.name === "data-engineer" ? 2 : 0, script_versions: 0, prompts: 0, shares: 0, webhook_sources: [], api_keys: 0, people: p.name === "analyst" ? ["ann@example.com", "bo@example.com"] : [] },
    unscoped: { signed_in_endpoints: 1 },
  };
}

const fileShares: Record<string, Share[]> = {};

function shareOf(resourceID: string, body: Record<string, string>): Share {
  return {
    id: `rshare-${Math.random().toString(36).slice(2, 8)}`,
    asset_id: "",
    resource_id: resourceID,
    token: "",
    created_by: "you@example.com",
    shared_with_email: body["shared_with_email"],
    shared_with_persona: body["shared_with_persona"],
    permission: (body["permission"] as Share["permission"]) || "viewer",
    access_mode: "restricted",
    revoked: false,
    access_count: 0,
    created_at: new Date().toISOString(),
  };
}

export const personaAccessHandlers = [
  http.post(`${ADMIN_BASE}/personas/effective-access`, async ({ request }) =>
    HttpResponse.json(evaluate((await request.json()) as PersonaCreateRequest)),
  ),

  // Registered ahead of /api/v1/resources/:id, which would read the name as an id.
  http.get("/api/v1/resources/shared-with-me", () => {
    const first = mockResources.resources[0];
    return HttpResponse.json({
      resources: first
        ? [{ resource: first, share_id: "rshare-demo", shared_by: "maria.chen@example.com", shared_at: "2026-10-20T15:04:00Z", permission: "viewer" }]
        : [],
    });
  }),
  http.get("/api/v1/resources/:id/shares", ({ params }) =>
    HttpResponse.json({ shares: fileShares[params["id"] as string] ?? [] }),
  ),
  http.post("/api/v1/resources/:id/shares", async ({ params, request }) => {
    const id = params["id"] as string;
    const share = shareOf(id, (await request.json()) as Record<string, string>);
    fileShares[id] = [share, ...(fileShares[id] ?? [])];
    return HttpResponse.json(share, { status: 201 });
  }),
  http.delete("/api/v1/resources/:id/shares/:shareID", ({ params }) => {
    const id = params["id"] as string;
    fileShares[id] = (fileShares[id] ?? []).filter((s) => s.id !== params["shareID"]);
    return new HttpResponse(null, { status: 204 });
  }),
  http.post("/api/v1/resources/:id/channel-shares", async ({ request }) => {
    const body = (await request.json()) as { channel: string };
    return HttpResponse.json({ channel: body.channel, queued: 1, detail: "queued" });
  }),
];
