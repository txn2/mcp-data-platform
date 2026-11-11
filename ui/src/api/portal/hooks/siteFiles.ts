import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { apiFetch, apiFetchRaw, ApiError } from "../client";
import type { SiteFile, SiteRelease } from "./sites";

// A site's files (#2590): dev's tree, one file's text, and the writes that
// make a new release of dev from the one the editor read. A save against an
// older release answers 409, which the editor turns into a conflict.

const one = (site: string) => `/sites/${encodeURIComponent(site)}`;
const siteKey = (site: string) => ["sites", site] as const;
const fileKeys = (site: string) => [...siteKey(site), "files"] as const;
const json = (method: string, body: unknown) => ({ method, body: JSON.stringify(body) });

function useInvalidate(site: string) {
  const qc = useQueryClient();
  return () => qc.invalidateQueries({ queryKey: siteKey(site) });
}

export interface SiteFileListing {
  environment: string;
  /** The release the listing was read from. */
  version: number;
  files: SiteFile[];
  total: number;
  truncated: boolean;
}

export interface SiteFileText {
  environment: string;
  release: number;
  path: string;
  content_type: string;
  body: string;
}

export interface SitePublished {
  release: SiteRelease;
  environment: string;
  address: string;
  unchanged?: boolean;
  /** How many served files a dry run would change: on a rendered site, the pages a layout touches. */
  outputs_changed?: number;
}

export interface SiteFileDiff {
  from: number;
  to: number;
  path: string;
  diff: string;
}

export function useSiteFiles(site: string, folder: string, enabled = true) {
  return useQuery({
    queryKey: [...fileKeys(site), "list", folder],
    enabled,
    retry: false,
    queryFn: () => apiFetch<SiteFileListing>(`${one(site)}/files?path=${encodeURIComponent(folder)}&limit=1000`),
  });
}

export function useSiteFileText(site: string, path: string, enabled = true) {
  return useQuery({
    queryKey: [...fileKeys(site), "text", path],
    enabled: enabled && !!path,
    retry: false,
    refetchOnWindowFocus: false,
    queryFn: () => apiFetch<SiteFileText>(`${one(site)}/file?path=${encodeURIComponent(path)}`),
  });
}

/** Reads a file's text once, outside the cache: what dev serves now, for a conflict. */
export function readSiteFile(site: string, path: string) {
  return apiFetch<SiteFileText>(`${one(site)}/file?path=${encodeURIComponent(path)}`);
}

export interface SaveFileBody {
  path: string;
  content: string;
  encoding?: "base64";
  base: number;
  note?: string;
  dry_run?: boolean;
}

export function useSaveSiteFile(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: SaveFileBody) => apiFetch<SitePublished>(`${one(site)}/file`, json("PUT", b)),
    onSuccess: (_out, b) => (b.dry_run ? undefined : invalidate()),
  });
}

export function useDeleteSiteFile(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: (b: { path: string; base: number; note?: string }) =>
      apiFetch<SitePublished>(
        `${one(site)}/file?path=${encodeURIComponent(b.path)}&base=${b.base}&note=${encodeURIComponent(b.note ?? "")}`,
        { method: "DELETE" },
      ),
    onSuccess: invalidate,
  });
}

export function useUploadSiteFiles(site: string) {
  const invalidate = useInvalidate(site);
  return useMutation({
    mutationFn: async (b: { files: File[]; folder: string; base: number; note?: string }) => {
      const form = new FormData();
      form.set("base", String(b.base));
      form.set("folder", b.folder);
      form.set("note", b.note ?? "");
      for (const f of b.files) form.append("files", f);
      const res = await apiFetchRaw(`${one(site)}/files`, { method: "POST", body: form });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new ApiError(res.status, body.detail ?? body.error ?? res.statusText);
      return body as SitePublished;
    },
    onSuccess: invalidate,
  });
}

export function useSiteFileDiff(site: string, path: string, from: number, to: number, enabled = true) {
  return useQuery({
    queryKey: [...fileKeys(site), "diff", path, from, to],
    enabled: enabled && !!path,
    retry: false,
    queryFn: () => apiFetch<SiteFileDiff>(`${one(site)}/file/diff?path=${encodeURIComponent(path)}&from=${from}&to=${to}`),
  });
}
