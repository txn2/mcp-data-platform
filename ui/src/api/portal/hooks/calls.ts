import { useQuery, useMutation, useQueryClient, keepPreviousData } from "@tanstack/react-query";
import { apiFetch } from "../client";
import {
  callListQuery,
  type CallListParams,
  type CallListResponse,
  type CallRecord,
} from "@/api/admin/types/calls";

// The caller's own recorded calls (#1321). The wire shape is the operator
// surface's, because it is the same catalog narrowed to one caller: the types
// are imported rather than restated so the two views cannot describe a record
// differently.
//
// There is no user parameter. The server scopes every read here to the
// authenticated caller and answers another user's record id as not-found, so a
// user facet would be a control that does nothing.

const myCallsKey = ["my-calls"] as const;

export function useMyCalls(params: Omit<CallListParams, "userId"> = {}) {
  return useQuery({
    queryKey: [...myCallsKey, params],
    queryFn: () => apiFetch<CallListResponse>(`/calls${callListQuery(params)}`),
    placeholderData: keepPreviousData,
  });
}

export function useMyCall(id: string) {
  return useQuery({
    queryKey: [...myCallsKey, "detail", id],
    queryFn: () => apiFetch<CallRecord>(`/calls/${encodeURIComponent(id)}`),
    enabled: Boolean(id),
  });
}

export function usePromoteMyCall() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<CallRecord>(`/calls/${encodeURIComponent(id)}/promote`, {
        method: "POST",
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: myCallsKey });
    },
  });
}

export function useRejectMyCall() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, note }: { id: string; note?: string }) =>
      apiFetch<CallRecord>(`/calls/${encodeURIComponent(id)}/reject`, {
        method: "POST",
        body: JSON.stringify({ note: note ?? "" }),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: myCallsKey });
    },
  });
}
