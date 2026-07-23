import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// PromptAttachment is one resource attached to a prompt as reference material
// (#1013). Everything past `attached_by` is absent when the link is broken or
// unreadable, mirroring what the server discloses.
export interface PromptAttachment {
  resource_id: string;
  position: number;
  attached_by?: string;
  display_name?: string;
  description?: string;
  category?: string;
  mime_type?: string;
  size_bytes?: number;
  uri?: string;
  scope?: "global" | "persona" | "user";
  scope_id?: string;
  // broken marks an attachment whose resource was deleted. The row survives so
  // the author can see and remove the dangling link.
  broken?: boolean;
  // unreadable marks an attachment that exists but is outside the caller's
  // scope; it carries no other metadata.
  unreadable?: boolean;
}

interface AttachmentListResponse {
  data: PromptAttachment[];
  total: number;
}

// AttachingPrompt names a prompt that depends on a resource, for the resource
// detail view's "used by" list.
export interface AttachingPrompt {
  id: string;
  name: string;
  display_name?: string;
  scope: string;
}

interface AttachingPromptsResponse {
  data: AttachingPrompt[];
  total: number;
}

const attachmentsKey = (promptId: string) => ["portal", "prompt-attachments", promptId];

// usePromptAttachments lists a prompt's attached materials in authored order.
// Disabled without a prompt id so the hook can be called unconditionally from a
// viewer that has not resolved its prompt yet.
export function usePromptAttachments(promptId: string | undefined) {
  return useQuery({
    queryKey: attachmentsKey(promptId ?? ""),
    enabled: Boolean(promptId),
    queryFn: () =>
      apiFetch<AttachmentListResponse>(`/prompts/${promptId}/attachments`),
  });
}

// useAttachResource attaches a resource. The server rejects an attachment
// narrower than the prompt with a 409 whose message names the resource, which
// the caller surfaces verbatim.
export function useAttachResource(promptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (resourceId: string) =>
      apiFetch<AttachmentListResponse>(`/prompts/${promptId}/attachments`, {
        method: "POST",
        body: JSON.stringify({ resource_id: resourceId }),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: attachmentsKey(promptId) });
    },
  });
}

export function useDetachResource(promptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (resourceId: string) =>
      apiFetch<{ status: string }>(
        `/prompts/${promptId}/attachments/${resourceId}`,
        { method: "DELETE" },
      ),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: attachmentsKey(promptId) });
    },
  });
}

// useReorderAttachments rewrites the authored order. Omitting an id detaches
// it, which is what lets a single save apply a reordered and pruned list.
export function useReorderAttachments(promptId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (resourceIds: string[]) =>
      apiFetch<AttachmentListResponse>(`/prompts/${promptId}/attachments`, {
        method: "PUT",
        body: JSON.stringify({ resource_ids: resourceIds }),
      }),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: attachmentsKey(promptId) });
    },
  });
}

// usePromptsUsingResource answers "what depends on this file?" for the resource
// detail view, so an operator sees the cost before deleting.
export function usePromptsUsingResource(resourceId: string | undefined) {
  return useQuery({
    queryKey: ["portal", "resource-prompts", resourceId ?? ""],
    enabled: Boolean(resourceId),
    queryFn: () =>
      apiFetch<AttachingPromptsResponse>(`/resources/${resourceId}/prompts`),
  });
}
