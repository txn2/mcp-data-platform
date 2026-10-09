import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw } from "../client";
import type { CurrentCode, Secret, SecretInput, SecretList } from "../types";

// Stored secrets (#2051).

const KEY = ["secrets"] as const;

export function useSecrets() {
  return useQuery({
    queryKey: KEY,
    queryFn: () => apiFetch<SecretList>("/secrets"),
  });
}

export function useSecret(name: string) {
  return useQuery({
    queryKey: [...KEY, name],
    queryFn: () => apiFetch<Secret>(`/secrets/${encodeURIComponent(name)}`),
    enabled: !!name,
  });
}

/** useSecretCode reads a totp secret's current code, only when enabled. */
export function useSecretCode(name: string, enabled: boolean) {
  return useQuery({
    queryKey: [...KEY, name, "code"],
    queryFn: () => apiFetch<CurrentCode>(`/secrets/${encodeURIComponent(name)}/code`),
    enabled: enabled && !!name,
    staleTime: 0,
    gcTime: 0,
  });
}

/** usePutSecret creates the secret or changes it; the name is in the path. */
export function usePutSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ name, body }: { name: string; body: SecretInput }) =>
      apiFetch<Secret>(`/secrets/${encodeURIComponent(name)}`, {
        method: "PUT",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}

export function useDeleteSecret() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (name: string) => {
      const res = await apiFetchRaw(`/secrets/${encodeURIComponent(name)}`, {
        method: "DELETE",
      });
      if (!res.ok) {
        const body = (await res.json().catch(() => ({}))) as {
          detail?: string;
        };
        throw new Error(
          body.detail ?? `Deleting the secret failed with status ${res.status}`,
        );
      }
    },
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: KEY });
    },
  });
}
