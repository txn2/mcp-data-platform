import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";
import type { PaginatedResponse } from "../types";

// ---------------------------------------------------------------------------
// Prompts
// ---------------------------------------------------------------------------

interface PortalPromptListResponse {
  personal: import("@/api/admin/types").Prompt[];
  available: import("@/api/admin/types").Prompt[];
}

export function useMyPrompts() {
  return useQuery({
    queryKey: ["portal", "prompts"],
    queryFn: () => apiFetch<PortalPromptListResponse>("/prompts"),
  });
}

// ScoredPrompt pairs a prompt with its relevance score, as returned by the
// ranked prompt search endpoint.
export interface ScoredPrompt {
  prompt: import("@/api/admin/types").Prompt;
  score: number;
}

// useSearchMyPrompts ranks approved prompts visible to the caller by relevance
// to query. Disabled (no request) until query is non-empty, so the list
// endpoint remains the default browse experience.
export function useSearchMyPrompts(query: string, params?: { limit?: number }) {
  const q = query.trim();
  const sp = new URLSearchParams({ q });
  if (params?.limit) sp.set("limit", String(params.limit));

  return useQuery({
    queryKey: ["search-my-prompts", q, params],
    enabled: q.length > 0,
    queryFn: () =>
      apiFetch<PaginatedResponse<ScoredPrompt>>(
        `/prompts/search?${sp.toString()}`,
      ),
  });
}

export function useCreateMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: {
      name: string;
      display_name?: string;
      description?: string;
      content: string;
      arguments?: { name: string; description: string; required: boolean }[];
      category?: string;
      tags?: string[];
    }) =>
      apiFetch<import("@/api/admin/types").Prompt>("/prompts", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

export function useUpdateMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: {
      id: string;
      name?: string;
      display_name?: string;
      description?: string;
      content?: string;
      category?: string;
      tags?: string[];
      arguments?: { name: string; description: string; required: boolean }[];
      requested_scope?: string;
      requested_personas?: string[];
    }) =>
      apiFetch<import("@/api/admin/types").Prompt>(`/prompts/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}

export function useDeleteMyPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/prompts/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["portal", "prompts"] });
    },
  });
}
