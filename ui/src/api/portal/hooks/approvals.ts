import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import { endpointKey } from "./scriptEndpoint";

// Approvals (#2437, #2494): a request for an administrator's decision. A form
// is served to callers with no session only at a version an administrator
// approved; its owner files the request, and administrators decide every
// request, their own included. The server enforces each rule; the pages only
// hide the controls a caller would be refused.

export type ApprovalStatus = "pending" | "approved" | "denied" | "withdrawn";

/** What changed since the version approved before, for the approver. */
export interface ApprovalChanges {
  approved_version: number;
  diff?: string;
  truncated?: boolean;
  added: number;
  removed: number;
}

export interface ApprovalRequest {
  id: string;
  subject_kind: "script_version" | "site_release" | "site_environment";
  purpose: "form_public" | "site_promote" | "site_public";
  script_id: string;
  script_name?: string;
  site_release_id?: string;
  site_name?: string;
  /** The environment a site release would serve, or that would become public. */
  target?: string;
  version: number;
  source_sha256: string;
  requester_email: string;
  requester_persona?: string;
  reason?: string;
  changes: ApprovalChanges;
  status: ApprovalStatus;
  decided_by?: string;
  decided_at?: string;
  decision_comment?: string;
  self_approved: boolean;
  created_at: string;
}

export const approvalsKey = ["approvals"] as const;

/** useApprovals lists the caller's own requests, or every one for an administrator. */
export function useApprovals(filter: { status?: ApprovalStatus | ""; scriptId?: string } = {}) {
  const q = new URLSearchParams();
  if (filter.status) q.set("status", filter.status);
  if (filter.scriptId) q.set("script_id", filter.scriptId);
  const query = q.toString();
  return useQuery({
    queryKey: [...approvalsKey, filter.status ?? "", filter.scriptId ?? ""],
    queryFn: () =>
      apiFetch<{ requests: ApprovalRequest[]; count: number }>(`/approvals${query ? `?${query}` : ""}`).then(
        (r) => r.requests,
      ),
  });
}

/** useRequestApproval asks the administrators to approve a version of a form. */
export function useRequestApproval(scriptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { version: number; reason: string }) =>
      apiFetch<ApprovalRequest>("/approvals", {
        method: "POST",
        body: JSON.stringify({ script: scriptId, version: input.version, reason: input.reason }),
      }),
    onSuccess: () => queryClient.invalidateQueries({ queryKey: approvalsKey }),
  });
}

/** useDecideApproval approves or denies a request. */
export function useDecideApproval() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (input: { id: string; decision: "approve" | "deny"; comment: string }) =>
      apiFetch<ApprovalRequest>(`/approvals/${input.id}/decide`, {
        method: "POST",
        body: JSON.stringify({ decision: input.decision, comment: input.comment }),
      }),
    onSuccess: (r) => {
      void queryClient.invalidateQueries({ queryKey: approvalsKey });
      void queryClient.invalidateQueries({ queryKey: endpointKey(r.script_id) });
    },
  });
}

/** OpenedPreview is a form's preview just opened: the address renders for the caller only. */
export interface OpenedPreview {
  id: string;
  url: string;
  expires_at: string;
}

/** useOpenFormPreview opens a preview of a saved version (the latest for 0) or of the unsaved source. */
export function useOpenFormPreview(scriptId: string) {
  return useMutation({
    mutationFn: (input: { version?: number; source?: string }) =>
      apiFetch<OpenedPreview>(`/scripts/${scriptId}/form-preview`, { method: "POST", body: JSON.stringify(input) }),
  });
}

/** definesForm reports whether source defines form_spec(req) and form_submit(req, values). */
export function definesForm(source: string): boolean {
  return /^def form_spec\(/m.test(source) && /^def form_submit\(/m.test(source);
}
