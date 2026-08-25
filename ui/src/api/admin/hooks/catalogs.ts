import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";

// ---------------------------------------------------------------------------
// API Gateway Catalogs (global OpenAPI spec bundles)
// ---------------------------------------------------------------------------

export interface APICatalogSummary {
  id: string;
  name: string;
  version?: string;
  display_name: string;
  description?: string;
  created_by?: string;
  created_at?: string;
  updated_at?: string;
  spec_count: number;
  ref_count: number;
}

export interface APICatalogSpec {
  spec_name: string;
  content?: string;
  // "embedded" specs are bundled from a connection's own toolkit and
  // re-seeded at every startup (see pkg/toolkits/apigateway/catalog
  // SourceEmbedded and seedAdminSelfConnection), so portal edits/deletes
  // do not persist; the portal treats them as read-only. Keep this union
  // in sync with the backend's source-kind constants.
  source_kind: "inline" | "upload" | "url" | "embedded";
  source_url?: string;
  etag?: string;
  // Operator-set per-spec URL prefix applied at api_list_endpoints
  // and api_invoke_endpoint time. Empty means "use whatever the
  // spec's servers[0].url declares"; explicit non-empty overrides
  // the derivation. See pkg/toolkits/apigateway/catalog
  // NormalizeBasePath for the validation rules.
  base_path?: string;
  // Operator-set per-spec summary overrides surfaced by api_list_specs
  // and the multi-spec gate on api_list_endpoints. Empty means "derive
  // from the spec's info.title / info.description". See catalog
  // NormalizeSpecTitle / NormalizeSpecDescription for the rules
  // (trimmed, no CR/LF/NUL, capped at 200 / 2000 chars).
  title?: string;
  description?: string;
  last_fetched_at?: string;
  created_at?: string;
  updated_at?: string;
  // Number of operations the spec content parses to (one of the
  // GET/POST/PUT/DELETE/PATCH/HEAD pairs in every path item).
  operation_count?: number;
  // Number of persisted embedding rows. Equal to operation_count
  // when fully indexed; less while a job is in flight or has
  // failed.
  embedding_count?: number;
  // Most recent embedding job's state (pending|running|
  // succeeded|failed). Empty when no job has run yet for this
  // spec.
  embedding_status?: string;
  // Attempt counter from the most recent job, surfaced as
  // "running (attempt N)" in the badge.
  embedding_attempts?: number;
  // Most recent job's last_error column. Non-empty only when
  // the job is on a retry or has failed terminally.
  embedding_last_error?: string;
}

// EmbeddingHealth is the catalog-level roll-up rendered at the
// top of the catalog editor. Operators check this before
// considering a catalog production-ready ("all specs indexed"
// or "3 pending, 1 failed").
export interface APICatalogEmbeddingHealth {
  catalog_id: string;
  specs_total: number;
  specs_indexed: number;
  specs_pending: number;
  specs_running: number;
  specs_failed: number;
}

// EmbeddingJob is one row from api_catalog_embedding_jobs.
// Exposed by the admin embedding-jobs endpoint so the portal
// can show per-spec history.
export interface APICatalogEmbeddingJob {
  id: number;
  catalog_id: string;
  spec_name: string;
  kind: string;
  status: string;
  attempts: number;
  last_error?: string;
  worker_id?: string;
  next_run_at?: string;
  lease_expires_at?: string;
  created_at?: string;
  started_at?: string;
  completed_at?: string;
}

// enabled is what keeps a non-administrator from asking: the route answers 403
// to them, and a surface that serves both audiences (the operation browser,
// #1478) must not spend a rejected request per mount to discover that.
export function useAPICatalogs(enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs"],
    queryFn: () => apiFetch<APICatalogSummary[]>("/api-catalogs"),
    enabled,
  });
}

export function useAPICatalog(id: string) {
  return useQuery({
    queryKey: ["api-catalogs", id],
    queryFn: () => apiFetch<APICatalogSummary>(`/api-catalogs/${id}`),
    enabled: !!id,
  });
}

export function useAPICatalogSpec(id: string, specName: string, enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs", id, "specs", specName],
    queryFn: () =>
      apiFetch<APICatalogSpec>(`/api-catalogs/${id}/specs/${specName}`),
    enabled: enabled && !!id && !!specName,
  });
}

export function useCreateAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      id: string;
      name: string;
      version?: string;
      display_name: string;
      description?: string;
    }) =>
      apiFetch<APICatalogSummary>("/api-catalogs", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useUpdateAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      ...body
    }: {
      id: string;
      name?: string;
      version?: string;
      display_name?: string;
      description?: string;
    }) =>
      apiFetch<APICatalogSummary>(`/api-catalogs/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useDeleteAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetchRaw(`/api-catalogs/${id}`, { method: "DELETE" }).then(async (res) => {
        if (!res.ok) {
          const body = await res.text();
          throw new Error(body || `delete failed: ${res.status}`);
        }
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useCloneAPICatalog() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      sourceID,
      ...body
    }: {
      sourceID: string;
      id: string;
      name?: string;
      version?: string;
      display_name?: string;
    }) =>
      apiFetch<APICatalogSummary>(`/api-catalogs/${sourceID}/clone`, {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}

export function useUpsertAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      catalogID,
      specName,
      ...body
    }: {
      catalogID: string;
      specName: string;
      source_kind: "inline" | "url";
      content?: string;
      source_url?: string;
      base_path?: string;
      title?: string;
      description?: string;
    }) =>
      apiFetch<APICatalogSpec>(
        `/api-catalogs/${catalogID}/specs/${specName}`,
        { method: "PUT", body: JSON.stringify(body) },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
    },
  });
}

export function useUploadAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({
      catalogID,
      specName,
      file,
      base_path,
      title,
      description,
    }: {
      catalogID: string;
      specName: string;
      file: File;
      base_path?: string;
      title?: string;
      description?: string;
    }) => {
      const form = new FormData();
      form.append("file", file);
      const params = new URLSearchParams();
      if (base_path && base_path.trim() !== "") params.set("base_path", base_path.trim());
      if (title && title.trim() !== "") params.set("title", title.trim());
      if (description && description.trim() !== "") params.set("description", description.trim());
      const qs = params.toString() ? `?${params.toString()}` : "";
      const res = await apiFetchRaw(
        `/api-catalogs/${catalogID}/specs/${specName}/upload${qs}`,
        { method: "PUT", body: form },
      );
      if (!res.ok) {
        const body = await res.text();
        throw new Error(body || `upload failed: ${res.status}`);
      }
      return (await res.json()) as APICatalogSpec;
    },
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
    },
  });
}

export function useRefreshAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ catalogID, specName }: { catalogID: string; specName: string }) =>
      apiFetch<APICatalogSpec>(
        `/api-catalogs/${catalogID}/specs/${specName}/refresh`,
        { method: "POST" },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
    },
  });
}

// useManualRetryEmbedding enqueues a manual_retry embedding job
// for the named spec. The button is an escape hatch (used only
// when an operator knows the dedup predicate's "same text,
// same model" check is wrong: model swapped externally, etc.).
// The automatic path (spec write enqueues a job; reconciler
// fills gaps) covers the common case without operator action.
//
// Returns 202 Accepted; the actual embedding happens off the
// request path. Caller polls the embedding health endpoint to
// see completion.
export function useManualRetryEmbedding() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ catalogID, specName }: { catalogID: string; specName: string }) =>
      apiFetch<{ status: string; created: boolean }>(
        `/api-catalogs/${catalogID}/specs/${specName}/reembed`,
        { method: "POST" },
      ),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "specs", vars.specName],
      });
      void qc.invalidateQueries({
        queryKey: ["api-catalogs", vars.catalogID, "embedding-health"],
      });
    },
  });
}

// useAPICatalogEmbeddingHealth polls the catalog-level
// embedding roll-up so the portal renders "all indexed" or
// "N pending, M failed" at the top of the catalog editor.
// Refetches every 5 seconds while the panel is mounted, since
// the worker runs off the request path and the operator needs
// the badge to reflect work as it completes.
export function useAPICatalogEmbeddingHealth(catalogID: string, enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs", catalogID, "embedding-health"],
    queryFn: () =>
      apiFetch<APICatalogEmbeddingHealth>(
        `/api-catalogs/${catalogID}/embedding-health`,
      ),
    enabled: enabled && !!catalogID,
    refetchInterval: 5000,
  });
}

// EmbeddingProviderStatus mirrors the server-side embeddingProviderStatusResponse:
// the platform-wide embedding provider's kind, model, dimension, and a
// health enum. status="unconfigured" indicates the noop placeholder is
// in use and semantic features are disabled (the portal renders a
// banner on the Catalogs and Memory panels in this state). See #429.
export interface EmbeddingProviderStatus {
  kind: string;
  model: string;
  dimension: number;
  status: "ok" | "unconfigured";
}

// useEmbeddingProviderStatus polls the platform-wide embedding-provider
// status. Used by the Catalogs panel and the Memory settings panel to
// surface a banner when the provider is unconfigured.
export function useEmbeddingProviderStatus() {
  return useQuery({
    queryKey: ["admin", "embedding", "status"],
    queryFn: () => apiFetch<EmbeddingProviderStatus>("/embedding/status"),
    refetchInterval: 30000,
  });
}

// useAPICatalogEmbeddingStatuses returns one row per spec. The
// portal renders these as per-spec badges in the CatalogsPanel.
// Refetched on the same 5s cadence as the health roll-up so the
// two views stay coherent.
export function useAPICatalogEmbeddingStatuses(catalogID: string, enabled = true) {
  return useQuery({
    queryKey: ["api-catalogs", catalogID, "embedding-statuses"],
    queryFn: () =>
      apiFetch<{ specs: APICatalogEmbeddingSpecStatus[] }>(
        `/api-catalogs/${catalogID}/embedding-status`,
      ),
    enabled: enabled && !!catalogID,
    refetchInterval: 5000,
  });
}

// APICatalogEmbeddingSpecStatus mirrors the server-side
// embeddingStatusResponse: one row per spec with operation /
// embedding counts plus the most recent job's state.
export interface APICatalogEmbeddingSpecStatus {
  spec_name: string;
  operation_count: number;
  embedding_count: number;
  // embedded_so_far is the worker's in-flight chunk-progress counter.
  // While job_status is "running" the badge renders this against
  // operation_count so a long embed pass shows incremental progress
  // instead of staying at 0/N until the final atomic upsert commits
  // embedding_count in one tick. See #430.
  embedded_so_far?: number;
  job_status?: string;
  job_attempts?: number;
  job_last_error?: string;
  job_updated_at?: string;
}

export function useDeleteAPICatalogSpec() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ catalogID, specName }: { catalogID: string; specName: string }) =>
      apiFetchRaw(`/api-catalogs/${catalogID}/specs/${specName}`, {
        method: "DELETE",
      }).then(async (res) => {
        if (!res.ok) {
          const body = await res.text();
          throw new Error(body || `delete failed: ${res.status}`);
        }
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["api-catalogs"] });
    },
  });
}
