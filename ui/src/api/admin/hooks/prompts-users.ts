import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
} from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type { PromptListResponse, Prompt } from "../types";
import { REFETCH_INTERVAL } from "./shared";

// ---------------------------------------------------------------------------
// Prompts
// ---------------------------------------------------------------------------

interface AdminPromptsParams {
  search?: string;
  scope?: string;
  owner_email?: string;
  review_requested?: boolean;
}

export function useAdminPrompts(params: AdminPromptsParams = {}) {
  const searchParams = new URLSearchParams();
  if (params.search) searchParams.set("search", params.search);
  if (params.scope) searchParams.set("scope", params.scope);
  if (params.owner_email) searchParams.set("owner_email", params.owner_email);
  if (params.review_requested) searchParams.set("review_requested", "true");

  const qs = searchParams.toString();
  return useQuery({
    queryKey: ["admin", "prompts", params],
    queryFn: () => apiFetch<PromptListResponse>(`/prompts${qs ? `?${qs}` : ""}`),
    refetchInterval: REFETCH_INTERVAL,
    placeholderData: keepPreviousData,
  });
}

export function useAdminPrompt(id: string | null) {
  return useQuery({
    queryKey: ["admin", "prompt", id],
    queryFn: () => apiFetch<Prompt>(`/prompts/${id}`),
    enabled: !!id,
  });
}

export function useCreateAdminPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (body: Partial<Prompt>) =>
      apiFetch<Prompt>("/prompts", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
    },
  });
}

export function useUpdateAdminPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...body }: { id: string } & Partial<Prompt>) =>
      apiFetch<Prompt>(`/prompts/${id}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
      queryClient.invalidateQueries({ queryKey: ["admin", "prompt"] });
    },
  });
}

export function useDeleteAdminPrompt() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch(`/prompts/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
    },
  });
}

export function useApprovePromptPromotion() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<Prompt>(`/prompts/${id}/approve`, { method: "POST" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
    },
  });
}

export function useRejectPromptPromotion() {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      apiFetch<Prompt>(`/prompts/${id}/reject`, { method: "POST" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["admin", "prompts"] });
    },
  });
}

// ---------------------------------------------------------------------------
// Memory
// ---------------------------------------------------------------------------

// --- Known-users directory (#614) ---

export function useDirectoryUsers(q?: string) {
  const query = q ? `?q=${encodeURIComponent(q)}` : "";
  return useQuery({
    queryKey: ["users", q ?? ""],
    queryFn: () =>
      apiFetch<import("../types").UserListResponse>(`/users${query}`),
  });
}

export function useCreateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: import("../types").UserCreateRequest) =>
      apiFetch<import("../types").DirectoryUser>("/users", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

export function useUpdateUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      email,
      ...body
    }: { email: string } & import("../types").UserUpdateRequest) =>
      apiFetch<import("../types").DirectoryUser>(
        `/users/${encodeURIComponent(email)}`,
        { method: "PUT", body: JSON.stringify(body) },
      ),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
}

export function useDeleteUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (email: string) =>
      apiFetchRaw(`/users/${encodeURIComponent(email)}`, {
        method: "DELETE",
      }).then((res) => {
        if (!res.ok) throw new Error("Failed to delete");
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["users"] });
    },
  });
}
