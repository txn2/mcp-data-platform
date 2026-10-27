import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { ChangesetListResponse } from "@/api/admin/types";

// The knowledge changesets, served to the portal for whoever holds
// apply_knowledge (#2337): the audience the Changesets tab is shown to. The
// admin API's routes are the administrator's, and a non-admin sent there is
// refused.

// How often the list refreshes, as the admin review lists do.
const CHANGESETS_REFETCH_INTERVAL = 30_000;

export interface ChangesetsParams {
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
    refetchInterval: CHANGESETS_REFETCH_INTERVAL,
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
