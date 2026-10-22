import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// Requests for access to an item a person cannot open (#2090).

export type AccessTargetType = "asset" | "collection" | "prompt";

export interface AccessRequest {
  id: string;
  target_type: AccessTargetType;
  target_id: string;
  requester_email: string;
  owner_email: string;
  message?: string;
  status: "pending" | "approved" | "declined" | "expired";
  permission?: string;
  created_at: string;
  expires_at: string;
  target_name?: string;
}

export interface AccessRequestPage {
  has_access: boolean;
  name?: string;
  owner_email: string;
  pending?: AccessRequest;
}

const segment = (t: AccessTargetType) => `${t}s`;

/** What the request page says for an item; an error when the item does not exist. */
export function useAccessRequestPage(type: AccessTargetType, id: string, enabled = true) {
  return useQuery({
    queryKey: ["access-request-page", type, id],
    queryFn: () => apiFetch<AccessRequestPage>(`/${segment(type)}/${id}/access-request`),
    enabled: enabled && id !== "",
    retry: false,
  });
}

export function useRequestAccess() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ type, id, message }: { type: AccessTargetType; id: string; message?: string }) =>
      apiFetch<AccessRequest>(`/${segment(type)}/${id}/access-requests`, {
        method: "POST",
        body: JSON.stringify(message ? { message } : {}),
      }),
    onSuccess: (_, v) => qc.invalidateQueries({ queryKey: ["access-request-page", v.type, v.id] }),
  });
}

/** The requests waiting on the caller, or on one item. */
export function useAccessRequests(target?: { type: AccessTargetType; id: string }, enabled = true) {
  const qs = target ? `?target_type=${target.type}&target_id=${encodeURIComponent(target.id)}` : "";
  return useQuery({
    queryKey: ["access-requests", target?.type ?? "", target?.id ?? ""],
    queryFn: () => apiFetch<{ requests: AccessRequest[] }>(`/access-requests${qs}`),
    enabled,
  });
}

export function useDecideAccessRequest() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, decision }: { id: string; decision: "viewer" | "editor" | "decline" }) =>
      decision === "decline"
        ? apiFetch<AccessRequest>(`/access-requests/${id}/decline`, { method: "POST" })
        : apiFetch<AccessRequest>(`/access-requests/${id}/approve`, {
            method: "POST",
            body: JSON.stringify({ permission: decision }),
          }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["access-requests"] });
      void qc.invalidateQueries({ queryKey: ["shares"] });
    },
  });
}
