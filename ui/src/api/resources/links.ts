import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ResourceApiError, resourceFetch, resourceFetchRaw } from "./client";

// Links to and from the library: an upload link an external service PUTs its
// files to (#2844), and a download link a connection fetches a file by
// (#2847).

/** An upload link that can still take a file, as its maker sees it. */
export interface UploadLink {
  id: string;
  scope: string;
  scope_id?: string;
  path: string;
  filename?: string;
  content_type: string;
  max_bytes: number;
  max_files: number;
  files_used: number;
  created_at: string;
  expires_at: string;
}

/** A link as it is made: the only time its URL is shown. */
export interface IssuedUploadLink {
  id: string;
  upload_url: string;
  method: string;
  expires_at: string;
  max_bytes: number;
  max_files: number;
  name_in_url?: boolean;
}

/** What the New upload link dialog asks for. */
export interface NewUploadLink {
  scope: string;
  scope_id?: string;
  path: string;
  filename?: string;
  content_type: string;
  size_bytes: number;
  max_files: number;
  ttl_seconds: number;
  if_exists?: "fail" | "replace";
}

/** The two kinds of download link. */
export type DownloadExpiry = "single_download" | "6h";

export interface DownloadLink {
  url: string;
  expires: DownloadExpiry;
  expires_at: string;
}

const linksKey = ["resources", "upload-links"] as const;

/** The caller's open upload links. */
export function useUploadLinks(enabled = true) {
  return useQuery({
    queryKey: linksKey,
    queryFn: () => resourceFetch<{ links: UploadLink[] }>("/upload-links").then((r) => r.links ?? []),
    enabled,
  });
}

/** Makes an upload link. */
export function useCreateUploadLink() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: NewUploadLink) =>
      resourceFetch<IssuedUploadLink>("/upload-links", { method: "POST", body: JSON.stringify(input) }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: linksKey }),
  });
}

/** Revokes an upload link. */
export function useRevokeUploadLink() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: async (id: string) => {
      const res = await resourceFetchRaw(`/upload-links/${encodeURIComponent(id)}/revoke`, { method: "POST" });
      if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new ResourceApiError(res.status, body.detail || body.error || res.statusText);
      }
    },
    onSuccess: () => void qc.invalidateQueries({ queryKey: linksKey }),
  });
}

/** A download link to one file. */
export function fetchDownloadLink(id: string, expires: DownloadExpiry): Promise<DownloadLink> {
  return resourceFetch<DownloadLink>(`/${encodeURIComponent(id)}/download-link?expires=${expires}`);
}
