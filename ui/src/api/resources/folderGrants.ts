import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "@/api/admin/client";
import { resourceFetch } from "./client";

// Write grants on Global folders (#2684): an administrator grants a person or
// a persona write on one Global folder and everything beneath it. The grants
// on a folder are the administrator's to read; a grantee reads only the
// folders they can write.

export interface FolderGrant {
  id: string;
  folder: string;
  email?: string;
  persona?: string;
  created_by: string;
  created_at: string;
  /** A grant on a folder above the one asked about. */
  inherited: boolean;
}

export interface FolderGrantList {
  folder: string;
  grants: FolderGrant[];
  /** The sites serving the folder, or one above it, as media. */
  sites: string[];
}

const grantsKey = ["resources", "folder-grants"] as const;

/** The Global folders the caller can write through a grant. */
export function useMyFolderGrants(enabled: boolean) {
  return useQuery({
    queryKey: [...grantsKey, "mine"],
    queryFn: () => resourceFetch<{ folders: string[] }>("/folder-grants/mine").then((r) => r.folders),
    enabled,
    staleTime: 30_000,
  });
}

/** The grants covering a Global folder, for an administrator. */
export function useFolderGrants(folder: string, enabled: boolean) {
  return useQuery({
    queryKey: [...grantsKey, folder],
    queryFn: () => apiFetch<FolderGrantList>(`/resources/folder-grants?folder=${encodeURIComponent(folder)}`),
    enabled: enabled && folder !== "",
  });
}

export function useAddFolderGrant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (body: { folder: string; email?: string; persona?: string }) =>
      apiFetch<FolderGrant>("/resources/folder-grants", { method: "POST", body: JSON.stringify(body) }),
    onSuccess: () => qc.invalidateQueries({ queryKey: grantsKey }),
  });
}

export function useRemoveFolderGrant() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) => apiFetch<FolderGrant>(`/resources/folder-grants/${encodeURIComponent(id)}`, { method: "DELETE" }),
    onSuccess: () => qc.invalidateQueries({ queryKey: grantsKey }),
  });
}

/** The Global folders a persona holds a write grant on, for its editor. */
export function usePersonaFolderGrants(persona: string) {
  return useQuery({
    queryKey: [...grantsKey, "persona", persona],
    queryFn: () => apiFetch<FolderGrantList>(`/resources/folder-grants?persona=${encodeURIComponent(persona)}`).then((r) => r.grants),
    enabled: persona !== "",
  });
}
