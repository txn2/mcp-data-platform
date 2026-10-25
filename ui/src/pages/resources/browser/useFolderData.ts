import { useMemo } from "react";
import { useFacets, useInfiniteResources } from "@/api/resources/hooks";
import type { Folder, ResourceSort } from "@/api/resources/types";
import type { ResourceRoot } from "../scopes";
import { fileEntries, folderEntries, sortFolders, type Entry } from "./model";

/** What the view is narrowed to: a search, whether it stays in the open folder, a tag. */
export interface Narrowing {
  query: string;
  within: boolean;
  tag: string;
}

/**
 * useFolderData is what the folder in view holds: its folders from the facets,
 * which count every depth, and its own files from the listing, paged (#1837).
 *
 * A search or a tag replaces the folder with hits, each carrying where it is
 * (#1530). A search looks in every library the caller reads, because search is
 * how a person finds a file they cannot place (#2293); with within set it looks
 * in the open folder and beneath it. A tag spans the whole top-level folder.
 * The root of a top-level folder lists no files: every file is filed in a
 * folder.
 */
export function useFolderData(root: ResourceRoot, path: string, narrowing: Narrowing, sort: ResourceSort) {
  const { query, tag } = narrowing;
  // The files shared with the caller have no folders to read; their view
  // lists the shares (#2258).
  const facets = useFacets(root.params, !root.shared);
  const flat = query !== "" || tag !== "";
  const listing = listsFiles(root, path, flat, searchesEverywhere(narrowing));
  const list = useInfiniteResources(listingParams(root, path, narrowing, sort), listing);

  const folders = useMemo(() => facets.data?.folders ?? [], [facets.data]);
  const resources = useMemo(() => (listing ? (list.data?.data ?? []) : []), [listing, list.data]);
  const entries: Entry[] = useMemo(() => {
    const dirs = flat ? [] : sortFolders(folderEntries(folders, path), sort);
    return [...dirs, ...fileEntries(resources)];
  }, [flat, folders, path, sort, resources]);
  const here = useMemo(() => (facets.data ? countAt(folders, path) : undefined), [facets.data, folders, path]);

  return {
    entries,
    folders,
    flat,
    /** How many files are at and beneath the folder in view. */
    here,
    /** How many files this level holds, loaded or not. */
    fileTotal: listing ? (list.data?.total ?? 0) : 0,
    isLoading: facets.isLoading || (listing && list.isLoading),
    hasNextPage: listing && list.hasNextPage,
    isFetchingNextPage: listing && list.isFetchingNextPage,
    fetchNextPage: list.fetchNextPage,
  };
}

/**
 * listsFiles reports whether a view lists files: a folder below a library's
 * root, or a search; in the files shared with the caller, whose view lists the
 * shares, only a search of every library.
 */
function listsFiles(root: ResourceRoot, path: string, flat: boolean, everywhere: boolean): boolean {
  if (root.shared) return everywhere;
  return flat || path !== "";
}

/** searchesEverywhere reports whether a view's search names no library. */
function searchesEverywhere(n: Narrowing): boolean {
  return n.query !== "" && !n.within;
}

/**
 * The listing request a view makes: one folder's own files, a search of every
 * library (no scope, which the server reads as every library the caller may
 * read), a search of the open folder and beneath it, or a tag's hits in the
 * top-level folder.
 */
export function listingParams(root: ResourceRoot, path: string, n: Narrowing, sort: ResourceSort) {
  const flat = n.query !== "" || n.tag !== "";
  const q = n.query || undefined;
  const tag = n.tag || undefined;
  if (searchesEverywhere(n)) return { direct: false, q, tag, sort };
  if (n.query !== "") return { ...root.params, path: path || undefined, direct: false, q, tag, sort };
  return { ...root.params, path: flat || path === "" ? undefined : path, direct: !flat, q, tag, sort };
}

/**
 * countAt is how many files a folder holds at every depth. Every resource sits
 * in a folder, so a top-level folder holds the sum of its first level.
 */
function countAt(folders: Folder[], path: string): number {
  if (path === "") return folders.filter((f) => !f.path.includes("/")).reduce((n, f) => n + f.count, 0);
  return folders.find((f) => f.path === path)?.count ?? 0;
}
