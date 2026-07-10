import { http, HttpResponse } from "msw";
import type {
  ConnectionInstance,
  EnrichmentRule,
  EnrichmentRuleBody,
  DryRunRequest,
  DryRunResponse,
} from "@/api/admin/types";
import {
  mockConnectionInstances,
  mockConnectionsOAuthHealth,
  mockConnectionOAuthStatus,
  mockConnectionAuthEvents,
  mockGatewayConnectionStatus,
} from "../data/connections";

// ADMIN_BASE mirrors handlers.ts. These handlers only cover the
// connection endpoints that are currently UNHANDLED there:
//   GET    /connection-instances                      (list)
//   GET    /connection-instances/:kind/:name          (detail)
//   PUT    /connection-instances/:kind/:name          (set)
//   DELETE /connection-instances/:kind/:name          (delete)
//   GET    /connections/oauth-health                  (bulk badge health)
//   GET    /connections/:kind/:name/oauth-status       (status card)
//   GET    /connections/:kind/:name/auth-events        (history timeline)
//   POST   /connections/:kind/:name/oauth-start
//   POST   /connections/:kind/:name/reacquire-oauth
//   GET    /gateway/connections/:name/status
//   POST   /gateway/connections/:name/test
//   POST   /gateway/connections/:name/refresh
//   POST   /gateway/connections/:name/reacquire-oauth
//   POST   /gateway/connections/:connection/enrichment-rules            (create)
//   PUT    /gateway/connections/:connection/enrichment-rules/:id        (update)
//   DELETE /gateway/connections/:connection/enrichment-rules/:id        (delete)
//   POST   /gateway/connections/:connection/enrichment-rules/:id/dry-run
//
// The GET list endpoints /connections and /connection-instances/effective, and
// GET /gateway/connections/:connection/enrichment-rules, are already handled in
// handlers.ts and are intentionally NOT duplicated here.
const ADMIN_BASE = "/api/v1/admin";

function findInstance(kind: string, name: string): ConnectionInstance | undefined {
  return mockConnectionInstances.find((c) => c.kind === kind && c.name === name);
}

export const connectionInstanceHandlers = [
  // --- Connection instances (DB-managed) ---
  http.get(`${ADMIN_BASE}/connection-instances`, () =>
    HttpResponse.json(mockConnectionInstances),
  ),

  http.get(`${ADMIN_BASE}/connection-instances/:kind/:name`, ({ params }) => {
    const kind = String(params["kind"]);
    const name = decodeURIComponent(String(params["name"]));
    const instance = findInstance(kind, name);
    if (!instance) return new HttpResponse(null, { status: 404 });
    return HttpResponse.json(instance);
  }),

  http.put(
    `${ADMIN_BASE}/connection-instances/:kind/:name`,
    async ({ params, request }) => {
      const kind = String(params["kind"]);
      const name = decodeURIComponent(String(params["name"]));
      const body = (await request.json().catch(() => ({}))) as {
        config?: Record<string, unknown>;
        description?: string;
      };
      const existing = findInstance(kind, name);
      const saved: ConnectionInstance = {
        kind,
        name,
        config: body.config ?? {},
        description: body.description ?? "",
        created_by: existing?.created_by ?? "admin@acme.example.com",
        updated_at: new Date().toISOString(),
      };
      return HttpResponse.json(saved);
    },
  ),

  http.delete(`${ADMIN_BASE}/connection-instances/:kind/:name`, () =>
    new HttpResponse(null, { status: 204 }),
  ),

  // --- Unified OAuth (any connection kind) ---
  http.get(`${ADMIN_BASE}/connections/oauth-health`, () =>
    HttpResponse.json(mockConnectionsOAuthHealth),
  ),

  http.get(
    `${ADMIN_BASE}/connections/:kind/:name/oauth-status`,
    ({ params }) => {
      const kind = String(params["kind"]);
      const name = decodeURIComponent(String(params["name"]));
      const status = mockConnectionOAuthStatus[`${kind}/${name}`];
      if (status) return HttpResponse.json(status);
      // Not an OAuth connection: report unconfigured so the card hides itself.
      return HttpResponse.json({
        configured: false,
        token_acquired: false,
        has_refresh_token: false,
      });
    },
  ),

  http.get(
    `${ADMIN_BASE}/connections/:kind/:name/auth-events`,
    ({ params }) => {
      const kind = String(params["kind"]);
      const name = decodeURIComponent(String(params["name"]));
      return HttpResponse.json(mockConnectionAuthEvents[`${kind}/${name}`] ?? []);
    },
  ),

  http.post(
    `${ADMIN_BASE}/connections/:kind/:name/oauth-start`,
    ({ params }) => {
      const kind = String(params["kind"]);
      const name = decodeURIComponent(String(params["name"]));
      const state = `mock-state-${kind}-${name}-${Date.now()}`;
      return HttpResponse.json({
        authorization_url: `https://auth.acme.example.com/oauth2/authorize?client_id=${kind}-${name}&state=${state}`,
        state,
        redirect_uri: `${window.location.origin}/api/v1/admin/connections/${kind}/${name}/oauth-callback`,
        expires_at: new Date(Date.now() + 10 * 60 * 1000).toISOString(),
      });
    },
  ),

  http.post(
    `${ADMIN_BASE}/connections/:kind/:name/reacquire-oauth`,
    () => new HttpResponse(null, { status: 204 }),
  ),

  // --- Gateway (mcp) runtime status + lifecycle ---
  http.get(`${ADMIN_BASE}/gateway/connections/:name/status`, ({ params }) => {
    const name = decodeURIComponent(String(params["name"]));
    const status = mockGatewayConnectionStatus[name];
    if (status) return HttpResponse.json(status);
    return HttpResponse.json({
      name,
      healthy: false,
      auth_mode: "none",
      tools: [],
    });
  }),

  http.post(`${ADMIN_BASE}/gateway/connections/:name/test`, ({ params }) => {
    const name = decodeURIComponent(String(params["name"]));
    const status = mockGatewayConnectionStatus[name];
    const tools = status?.tools ?? ["probe_tool_a", "probe_tool_b"];
    return HttpResponse.json({
      healthy: true,
      tools: tools.map((t) => ({
        name: t,
        local_name: `${name}_${t}`,
        description: `Proxied ${t} from ${name}.`,
      })),
    });
  }),

  http.post(`${ADMIN_BASE}/gateway/connections/:name/refresh`, ({ params }) => {
    const name = decodeURIComponent(String(params["name"]));
    const status = mockGatewayConnectionStatus[name];
    return HttpResponse.json({
      healthy: true,
      tools: status?.tools ?? [],
    });
  }),

  http.post(
    `${ADMIN_BASE}/gateway/connections/:name/reacquire-oauth`,
    ({ params }) => {
      const name = decodeURIComponent(String(params["name"]));
      const status = mockGatewayConnectionStatus[name];
      if (status) return HttpResponse.json(status);
      return HttpResponse.json({
        name,
        healthy: true,
        auth_mode: "oauth",
        tools: [],
      });
    },
  ),

  // --- Enrichment-rule mutations (GET is handled in handlers.ts) ---
  http.post(
    `${ADMIN_BASE}/gateway/connections/:connection/enrichment-rules`,
    async ({ params, request }) => {
      const connection = decodeURIComponent(String(params["connection"]));
      const body = (await request.json().catch(() => ({}))) as EnrichmentRuleBody;
      const nowIso = new Date().toISOString();
      const rule: EnrichmentRule = {
        id: `enr-${connection}-${Date.now()}`,
        connection_name: connection,
        tool_name: body.tool_name,
        when_predicate: body.when_predicate,
        enrich_action: body.enrich_action,
        merge_strategy: body.merge_strategy,
        description: body.description,
        enabled: body.enabled,
        created_by: "admin@acme.example.com",
        created_at: nowIso,
        updated_at: nowIso,
      };
      return HttpResponse.json(rule, { status: 201 });
    },
  ),

  http.put(
    `${ADMIN_BASE}/gateway/connections/:connection/enrichment-rules/:id`,
    async ({ params, request }) => {
      const connection = decodeURIComponent(String(params["connection"]));
      const id = String(params["id"]);
      const body = (await request.json().catch(() => ({}))) as EnrichmentRuleBody;
      const rule: EnrichmentRule = {
        id,
        connection_name: connection,
        tool_name: body.tool_name,
        when_predicate: body.when_predicate,
        enrich_action: body.enrich_action,
        merge_strategy: body.merge_strategy,
        description: body.description,
        enabled: body.enabled,
        created_by: "admin@acme.example.com",
        created_at: "2025-01-08T14:22:00Z",
        updated_at: new Date().toISOString(),
      };
      return HttpResponse.json(rule);
    },
  ),

  http.delete(
    `${ADMIN_BASE}/gateway/connections/:connection/enrichment-rules/:id`,
    () => new HttpResponse(null, { status: 204 }),
  ),

  http.post(
    `${ADMIN_BASE}/gateway/connections/:connection/enrichment-rules/:id/dry-run`,
    async ({ params, request }) => {
      const id = String(params["id"]);
      const body = (await request.json().catch(() => ({}))) as DryRunRequest;
      const response: DryRunResponse = {
        // Echo the caller's sample response with an enrichment appended so the
        // preview shows a plausible merged result.
        response: {
          ...(typeof body.response === "object" && body.response !== null
            ? (body.response as Record<string, unknown>)
            : { input: body.response }),
          semantic: {
            source: "acme-catalog",
            owners: ["data-platform@acme.example.com"],
            glossary_terms: ["CustomerAccount"],
          },
        },
        warnings: [],
        fired: [
          {
            rule_id: id,
            source: "acme-catalog",
            op: "datahub_get_entity",
            duration_ms: 34,
          },
        ],
      };
      return HttpResponse.json(response);
    },
  ),
];
