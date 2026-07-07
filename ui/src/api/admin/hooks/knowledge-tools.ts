import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { apiFetch } from "../client";
import type {
  InsightListResponse,
  InsightStats,
  ChangesetListResponse,
  ToolSchemaMap,
  ToolCallRequest,
  ToolCallResponse,
  ToolDetail,
  ToolVisibilityRequest,
  ToolVisibilityResponse,
} from "../types";
import { REFETCH_INTERVAL } from "./shared";

// ---------------------------------------------------------------------------
// Knowledge — Insights & Changesets
// ---------------------------------------------------------------------------

interface InsightsParams {
  page?: number;
  perPage?: number;
  status?: string;
  category?: string;
  confidence?: string;
  entityUrn?: string;
  capturedBy?: string;
  /** "oldest" sorts oldest-first (stalest review debt first); default newest-first. */
  order?: "oldest" | "newest";
}

export function useInsights(params: InsightsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.perPage) searchParams.set("per_page", String(params.perPage));
  if (params.status) searchParams.set("status", params.status);
  if (params.category) searchParams.set("category", params.category);
  if (params.confidence) searchParams.set("confidence", params.confidence);
  if (params.entityUrn) searchParams.set("entity_urn", params.entityUrn);
  if (params.capturedBy) searchParams.set("captured_by", params.capturedBy);
  if (params.order) searchParams.set("order", params.order);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["knowledge", "insights", params],
    queryFn: () =>
      apiFetch<InsightListResponse>(
        `/knowledge/insights${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useInsightStats(options?: { enabled?: boolean }) {
  return useQuery({
    queryKey: ["knowledge", "insights", "stats"],
    queryFn: () => apiFetch<InsightStats>("/knowledge/insights/stats"),
    refetchInterval: REFETCH_INTERVAL,
    // Gated off for callers without review access (the stats endpoint is
    // admin-scoped), so a non-reviewer never fires a 403 just to size a badge.
    enabled: options?.enabled ?? true,
  });
}

export function useUpdateInsightStatus() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      status,
      reviewNotes,
    }: {
      id: string;
      status: string;
      reviewNotes?: string;
    }) =>
      apiFetch(`/knowledge/insights/${id}/status`, {
        method: "PUT",
        body: JSON.stringify({ status, review_notes: reviewNotes }),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["knowledge"] });
    },
  });
}

interface ChangesetsParams {
  page?: number;
  perPage?: number;
  entityUrn?: string;
  appliedBy?: string;
  rolledBack?: string;
}

export function useChangesets(params: ChangesetsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.page) searchParams.set("page", String(params.page));
  if (params.perPage) searchParams.set("per_page", String(params.perPage));
  if (params.entityUrn) searchParams.set("entity_urn", params.entityUrn);
  if (params.appliedBy) searchParams.set("applied_by", params.appliedBy);
  if (params.rolledBack) searchParams.set("rolled_back", params.rolledBack);

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["knowledge", "changesets", params],
    queryFn: () =>
      apiFetch<ChangesetListResponse>(
        `/knowledge/changesets${qs ? `?${qs}` : ""}`,
      ),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useRollbackChangeset() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/knowledge/changesets/${id}/rollback`, {
        method: "POST",
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["knowledge"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Tools — Schema & Execution
// ---------------------------------------------------------------------------

export function useToolSchemas() {
  return useQuery({
    queryKey: ["tools", "schemas"],
    queryFn: () => apiFetch<ToolSchemaMap>("/tools/schemas"),
    staleTime: 5 * 60_000,
  });
}

export function useCallTool() {
  return useMutation({
    mutationFn: (req: ToolCallRequest) =>
      apiFetch<ToolCallResponse>("/tools/call", {
        method: "POST",
        body: JSON.stringify(req),
      }),
  });
}

// Aggregating per-tool detail used by the Tools master-detail page.
export function useToolDetail(name: string | null) {
  return useQuery({
    queryKey: ["tools", "detail", name],
    queryFn: () => apiFetch<ToolDetail>(`/tools/${encodeURIComponent(name!)}`),
    enabled: !!name,
  });
}

// Save a per-tool description override. Empty value reverts to default.
export function useUpdateToolDescription(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (value: string) =>
      apiFetch(
        `/config/entries/${encodeURIComponent(`tool.${name}.description`)}`,
        {
          method: "PUT",
          body: JSON.stringify({ value }),
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tools", "detail", name] });
      queryClient.invalidateQueries({ queryKey: ["tools"] });
    },
  });
}

// Remove an existing description override (revert to default).
export function useResetToolDescription(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: () =>
      apiFetch(
        `/config/entries/${encodeURIComponent(`tool.${name}.description`)}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tools", "detail", name] });
      queryClient.invalidateQueries({ queryKey: ["tools"] });
    },
  });
}

// Toggle a tool's membership in the platform-wide tools.deny list.
export function useSetToolVisibility(name: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (req: ToolVisibilityRequest) =>
      apiFetch<ToolVisibilityResponse>(
        `/tools/${encodeURIComponent(name)}/visibility`,
        {
          method: "PUT",
          body: JSON.stringify(req),
        },
      ),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["tools", "detail", name] });
      queryClient.invalidateQueries({ queryKey: ["tools"] });
      // Other admin surfaces still derive hidden state from /connections.
      queryClient.invalidateQueries({ queryKey: ["connections"] });
    },
  });
}
