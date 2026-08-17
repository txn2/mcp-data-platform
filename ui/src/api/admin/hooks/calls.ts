import { useQuery, useMutation, useQueryClient, keepPreviousData } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { callListQuery, type CallListParams, type CallListResponse, type CallRecord } from "../types/calls";

// The call catalog, operator side (#1321): every recorded query and API
// invocation, the review queue of the ones worth publishing, and the two
// decisions a reviewer can make about one.

// callsKey roots every call query key, so one invalidation after a decision
// refreshes the queue, the listing and the open record together — promoting
// changes all three.
const callsKey = ["admin", "calls"] as const;

export function useCalls(params: CallListParams = {}) {
  return useQuery({
    queryKey: [...callsKey, params],
    queryFn: () => apiFetch<CallListResponse>(`/calls${callListQuery(params)}`),
    // No poll: a record is read after the fact. Paging must not blank the
    // table meanwhile.
    placeholderData: keepPreviousData,
  });
}

export function useCall(id: string) {
  return useQuery({
    queryKey: [...callsKey, "detail", id],
    queryFn: () => apiFetch<CallRecord>(`/calls/${encodeURIComponent(id)}`),
    enabled: Boolean(id),
  });
}

export function usePromoteCall() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<CallRecord>(`/calls/${encodeURIComponent(id)}/promote`, {
        method: "POST",
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: callsKey });
    },
  });
}

export function useRejectCall() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, note }: { id: string; note?: string }) =>
      apiFetch<CallRecord>(`/calls/${encodeURIComponent(id)}/reject`, {
        method: "POST",
        body: JSON.stringify({ note: note ?? "" }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: callsKey });
    },
  });
}
