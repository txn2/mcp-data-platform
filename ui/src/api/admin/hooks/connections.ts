import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type { ConnectionInstance } from "../types";
import { REFETCH_INTERVAL } from "./shared";

// ---------------------------------------------------------------------------
// Connection Instances (DB-managed)
// ---------------------------------------------------------------------------

export function useConnectionInstances() {
  return useQuery({
    queryKey: ["connection-instances"],
    queryFn: () => apiFetch<ConnectionInstance[]>("/connection-instances"),
  });
}

export function useConnectionInstance(kind: string, name: string) {
  return useQuery({
    queryKey: ["connection-instances", kind, name],
    queryFn: () => apiFetch<ConnectionInstance>(`/connection-instances/${kind}/${name}`),
    enabled: !!kind && !!name,
  });
}

export function useSetConnectionInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, name, ...body }: { kind: string; name: string; config: Record<string, unknown>; description?: string }) =>
      apiFetch<ConnectionInstance>(`/connection-instances/${kind}/${name}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["connection-instances"] });
      void qc.invalidateQueries({ queryKey: ["connections"] });
    },
  });
}

export function useDeleteConnectionInstance() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, name }: { kind: string; name: string }) =>
      apiFetchRaw(`/connection-instances/${kind}/${name}`, { method: "DELETE" }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["connection-instances"] });
      void qc.invalidateQueries({ queryKey: ["connections"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Gateway-specific endpoints (test/refresh + enrichment rules)
// ---------------------------------------------------------------------------

export function useTestGatewayConnection() {
  return useMutation({
    mutationFn: ({ name, config }: { name: string; config: Record<string, unknown> }) =>
      apiFetch<import("../types").GatewayTestResponse>(
        `/gateway/connections/${name}/test`,
        { method: "POST", body: JSON.stringify({ config }) },
      ),
  });
}

export function useRefreshGatewayConnection() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<import("../types").GatewayRefreshResponse>(
        `/gateway/connections/${name}/refresh`,
        { method: "POST" },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["connections"] });
      void qc.invalidateQueries({ queryKey: ["tools"] });
    },
  });
}

export function useGatewayConnectionStatus(name: string, enabled = true) {
  return useQuery({
    queryKey: ["gateway-status", name],
    queryFn: () =>
      apiFetch<import("../types").GatewayConnectionStatus>(
        `/gateway/connections/${name}/status`,
      ),
    enabled: enabled && !!name,
    refetchInterval: REFETCH_INTERVAL,
  });
}

export function useReacquireGatewayOAuth() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<import("../types").GatewayConnectionStatus>(
        `/gateway/connections/${name}/reacquire-oauth`,
        { method: "POST" },
      ),
    onSuccess: (_, name) => {
      void qc.invalidateQueries({ queryKey: ["gateway-status", name] });
    },
  });
}

// useStartConnectionOAuth kicks off the authorization_code flow for
// the given kind ("mcp", "api", future kinds). The backend dispatches
// on the kind path parameter to the corresponding OAuthKindHandler.
// Replaces the prior per-kind useStartGatewayOAuth /
// useStartAPIGatewayOAuth hooks that targeted divergent routes against
// divergent token stores — every kind now shares one
// /connections/{kind}/{name}/oauth-start route backed by the unified
// connoauth.Store.
export function useStartConnectionOAuth(kind: string) {
  return useMutation({
    mutationFn: ({ name, returnURL }: { name: string; returnURL?: string }) =>
      apiFetch<import("../types").GatewayOAuthStartResponse>(
        `/connections/${kind}/${name}/oauth-start`,
        {
          method: "POST",
          body: JSON.stringify({ return_url: returnURL ?? "" }),
        },
      ),
  });
}

// useStartGatewayOAuth is the MCP-kind specialization of
// useStartConnectionOAuth. Kept as a named export so the existing MCP
// gateway form callsite is unchanged through this refactor.
export function useStartGatewayOAuth() {
  return useStartConnectionOAuth("mcp");
}

// useStartAPIGatewayOAuth is the API-kind specialization of
// useStartConnectionOAuth. Kept as a named export so the existing
// HTTP API gateway form callsite is unchanged through this refactor.
export function useStartAPIGatewayOAuth() {
  return useStartConnectionOAuth("api");
}

// useConnectionsOAuthHealth polls the bulk OAuth-health endpoint on
// a 10-second cadence. Returns one row per connection with the bits
// the connection-list view needs to render a per-row badge without
// fanning out N per-row /oauth-status calls. Polling interval
// matches useConnectionOAuthStatus so a long-stale failure surface
// is bounded the same way the per-connection card is.
export function useConnectionsOAuthHealth() {
  return useQuery({
    queryKey: ["connections-oauth-health"],
    queryFn: () =>
      apiFetch<import("../types").ConnectionsOAuthHealthResponse>(
        `/connections/oauth-health`,
      ),
    refetchInterval: 10000,
  });
}

// useConnectionOAuthStatus returns the unified OAuth status snapshot
// for ANY connection kind. Renders the status card in both the MCP
// gateway and HTTP API gateway connection views.
export function useConnectionOAuthStatus(kind: string, name: string, enabled = true) {
  return useQuery({
    queryKey: ["connection-oauth-status", kind, name],
    queryFn: () =>
      apiFetch<import("../types").ConnectionOAuthStatus>(
        `/connections/${kind}/${name}/oauth-status`,
      ),
    enabled: enabled && !!kind && !!name,
    refetchInterval: 10000,
  });
}

// useConnectionAuthEvents returns the most recent 30 OAuth-lifecycle
// events for the given connection. Powers the History panel under the
// OAuth status card so operators can see why a token vanished without
// reading pod logs.
export function useConnectionAuthEvents(kind: string, name: string, enabled = true) {
  return useQuery({
    queryKey: ["connection-auth-events", kind, name],
    queryFn: () =>
      apiFetch<import("../types").ConnectionAuthEvent[]>(
        `/connections/${kind}/${name}/auth-events`,
      ),
    enabled: enabled && !!kind && !!name,
    refetchInterval: 30000,
  });
}

// useReacquireConnectionOAuth forces a refresh-token exchange for ANY
// connection kind. Useful from the admin status card to verify the
// persisted refresh token still works against the IdP.
export function useReacquireConnectionOAuth() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ kind, name }: { kind: string; name: string }) =>
      apiFetch<void>(`/connections/${kind}/${name}/reacquire-oauth`, {
        method: "POST",
      }),
    onSuccess: (_, { kind, name }) => {
      void qc.invalidateQueries({
        queryKey: ["connection-oauth-status", kind, name],
      });
    },
  });
}

export function useEnrichmentRules(connection: string, enabled = true) {
  return useQuery({
    queryKey: ["enrichment-rules", connection],
    queryFn: () =>
      apiFetch<import("../types").EnrichmentRule[]>(
        `/gateway/connections/${connection}/enrichment-rules`,
      ),
    enabled: enabled && !!connection,
  });
}

export function useCreateEnrichmentRule(connection: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: import("../types").EnrichmentRuleBody) =>
      apiFetch<import("../types").EnrichmentRule>(
        `/gateway/connections/${connection}/enrichment-rules`,
        { method: "POST", body: JSON.stringify(body) },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["enrichment-rules", connection] });
    },
  });
}

export function useUpdateEnrichmentRule(connection: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & import("../types").EnrichmentRuleBody) =>
      apiFetch<import("../types").EnrichmentRule>(
        `/gateway/connections/${connection}/enrichment-rules/${id}`,
        { method: "PUT", body: JSON.stringify(body) },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["enrichment-rules", connection] });
    },
  });
}

export function useDeleteEnrichmentRule(connection: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetchRaw(`/gateway/connections/${connection}/enrichment-rules/${id}`, {
        method: "DELETE",
      }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete enrichment rule");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["enrichment-rules", connection] });
    },
  });
}

export function useDryRunEnrichmentRule(connection: string) {
  return useMutation({
    mutationFn: ({ id, body }: { id: string; body: import("../types").DryRunRequest }) =>
      apiFetch<import("../types").DryRunResponse>(
        `/gateway/connections/${connection}/enrichment-rules/${id}/dry-run`,
        { method: "POST", body: JSON.stringify(body) },
      ),
  });
}
