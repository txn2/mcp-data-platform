import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// --- My own API keys (#1759) ---
//
// A key issued here authenticates as its owner and carries the roles they
// hold, so a client that speaks only bearer tokens reaches the platform as the
// same identity their signed-in session does. There is deliberately no roles
// field: a person cannot widen their own key.

/** MyAPIKey is one of the caller's keys. A key value is never listed. */
export interface MyAPIKey {
  name: string;
  description?: string;
  roles: string[];
  expires_at?: string;
  expired?: boolean;
}

export interface MyAPIKeyListResponse {
  keys: MyAPIKey[];
  total: number;
}

export interface MyAPIKeyCreateRequest {
  name: string;
  description?: string;
  /** A Go duration ("720h"). Left out, the key lasts until it is revoked. */
  expires_in?: string;
}

/** MyAPIKeyCreated carries the key value, which is readable only here. */
export interface MyAPIKeyCreated extends MyAPIKey {
  key: string;
  warning: string;
}

const myAPIKeysKey = ["portal", "api-keys"];

export function useMyAPIKeys() {
  return useQuery({
    queryKey: myAPIKeysKey,
    queryFn: () => apiFetch<MyAPIKeyListResponse>("/api-keys"),
  });
}

export function useCreateMyAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: MyAPIKeyCreateRequest) =>
      apiFetch<MyAPIKeyCreated>("/api-keys", {
        method: "POST",
        body: JSON.stringify(body),
      }),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: myAPIKeysKey });
    },
  });
}

export function useRevokeMyAPIKey() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (name: string) =>
      apiFetch<{ status: string }>(`/api-keys/${encodeURIComponent(name)}`, {
        method: "DELETE",
      }),
    // The list is refetched on failure too: a revoke that raced another
    // session's must not leave the page showing a key that is already gone.
    onSettled: () => {
      void qc.invalidateQueries({ queryKey: myAPIKeysKey });
    },
  });
}
