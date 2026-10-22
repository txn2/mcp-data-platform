import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ApiError, apiFetch, apiFetchRaw } from "../client";
import type { ExternalAccountSettings, ExternalUser, ExternalUserList, NewExternalUser } from "../types";

// External accounts (#2092).

const USERS = ["external-users"] as const;
const SETTINGS = ["settings", "external-accounts"] as const;

/** The external users; an error when the deployment has no external accounts. */
export function useExternalUsers() {
  return useQuery({
    queryKey: USERS,
    queryFn: () => apiFetch<ExternalUserList>("/external-users"),
    retry: false,
  });
}

export function useCreateExternalUser() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: NewExternalUser) =>
      apiFetch<ExternalUser>("/external-users", { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: USERS }),
  });
}

/** One action on an account: extend (with the new expiry, or null), suspend, resume or remove. */
export function useExternalUserAction() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async ({ id, action, expiresAt }: { id: string; action: "extend" | "suspend" | "resume" | "remove"; expiresAt?: string | null }) => {
      const path = `/external-users/${encodeURIComponent(id)}`;
      // Each action answers 204 with no body.
      const res =
        action === "remove"
          ? await apiFetchRaw(path, { method: "DELETE" })
          : await apiFetchRaw(`${path}/${action}`, {
              method: "POST",
              headers: { "Content-Type": "application/json" },
              body: action === "extend" ? JSON.stringify({ expires_at: expiresAt ?? null }) : undefined,
            });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as { detail?: string };
        throw new ApiError(res.status, body.detail || `Request failed with status ${res.status}`);
      }
    },
    onSuccess: () => qc.invalidateQueries({ queryKey: USERS }),
  });
}

export function useExternalAccountSettings() {
  return useQuery({
    queryKey: SETTINGS,
    queryFn: () => apiFetch<ExternalAccountSettings>("/settings/external-accounts"),
  });
}

export function useSetExternalAccountSettings() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: ExternalAccountSettings) =>
      apiFetch<ExternalAccountSettings>("/settings/external-accounts", { method: "PUT", body: JSON.stringify(input) }),
    onSuccess: (data) => qc.setQueryData(SETTINGS, data),
  });
}
