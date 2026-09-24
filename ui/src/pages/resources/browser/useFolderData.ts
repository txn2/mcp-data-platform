import { useMemo } from "react";
import { useFacets, useInfiniteResources } from "@/api/resources/hooks";
import type { Folder, ResourceSort } from "@/api/resources/types";
import type { ResourceRoot } from "../scopes";
import { fileEntries, folderEntries, sortFolders, type Entry } from "./model";

/**
 * useFolderData is what the folder in view holds: its folders from the facets,
 * which count every depth, and its own files from the listing, paged (#1837).
 *
 * A search or a tag replaces the folder with hits from the whole top-level
 * folder, each carrying where it is, because a search that looked only in the
 * open folder would make the tree worse than a flat list (#1530). The root of a
 * top-level folder lists no files: every file is filed in a folder.
 */
export function useFolderData(
  root: ResourceRoot,
  path: string,
  query: string,
  tag: string,
  sort: ResourceSort,
) {
  const facets = useFacets(root.params);
  const flat = query !== "" || tag !== "";
  const listing = flat || path !== "";
  const list = useInfiniteResources(listingParams(root, path, query, tag, sort), listing);

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

/** The listing request a view makes: one folder's own files, or a search's hits. */
function listingParams(root: ResourceRoot, path: string, query: string, tag: string, sort: ResourceSort) {
  const flat = query !== "" || tag !== "";
  return {
    ...root.params,
    path: flat || path === "" ? undefined : path,
    direct: !flat,
    q: query || undefined,
    tag: tag || undefined,
    sort,
  };
}

/**
 * countAt is how many files a folder holds at every depth. Every resource sits
 * in a folder, so a top-level folder holds the sum of its first level.
 */
function countAt(folders: Folder[], path: string): number {
  if (path === "") return folders.filter((f) => !f.path.includes("/")).reduce((n, f) => n + f.count, 0);
  return folders.find((f) => f.path === path)?.count ?? 0;
}
