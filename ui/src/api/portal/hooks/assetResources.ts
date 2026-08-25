import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// The managed resources an asset's content references (#1475), from both ends:
// the asset's own list, and the resource's answer to what is holding it up.
//
// An asset names a resource by its mcp:// URI and the platform rewrites that
// URI as it serves (#1474). These hooks manage the declaration behind that
// rewrite; they never touch the asset's content, which is why the panel has to
// hand the author the URI to paste.

// AssetResourceRef is one referenced resource as the sidebar renders it.
export interface AssetResourceRef {
  resource_id: string;
  uri: string;
  position: number;
  declared_by?: string;
  display_name?: string;
  filename?: string;
  description?: string;
  category?: string;
  mime_type?: string;
  size_bytes?: number;
  scope?: "global" | "persona" | "user";
  scope_id?: string;
  // content_url is the reference's own serving URL, the same one the platform
  // writes into the content this reader is served. The panel loads a thumbnail
  // through it rather than through the resource route, which a reader of a
  // shared asset may not be allowed to call.
  content_url?: string;
  // broken marks a reference whose resource was deleted. The row survives so
  // the owner can see that the asset is serving without that file.
  broken?: boolean;
  // readable is whether this reader could open the resource on its own, as
  // opposed to through the asset. It decides whether the row is a link: a
  // reader of a shared asset can see a file they have no direct access to.
  readable?: boolean;
  // occurrences names where the asset's stored content still writes this URI.
  // Empty means the content does not name it, or could not be read.
  occurrences?: RefOccurrence[];
}

// RefOccurrence is one line of the stored content that writes a reference's
// URI. A line naming it twice is one occurrence: the reader is being pointed at
// a place to edit, and the place is the line.
export interface RefOccurrence {
  line: number;
  snippet: string;
  // truncated is set on the last reported line when the scan hit its cap, so a
  // warning built from the list reads as "at least these".
  truncated?: boolean;
}

// RefAudience is how widely the asset is shared, which is what a reference
// gives the referenced file.
export interface RefAudience {
  public: boolean;
  shared_with_users: boolean;
}

export interface AssetResourceRefsResponse {
  data: AssetResourceRef[];
  total: number;
  audience: RefAudience;
  // can_edit is the server's answer on this reader's authority. The panel
  // offers add and remove on it rather than re-deriving ownership here, so the
  // control a reader sees and the answer the route gives cannot differ.
  can_edit: boolean;
  max: number;
  // notice is the sentence stating what a reference gives away, authored once
  // on the server so the person and the agent are told the same thing.
  notice: string;
  // content_scanned reports whether the asset's stored content was read to find
  // where it writes each URI. False means the occurrence lists say nothing at
  // all -- the content is binary, too large, or could not be read -- which is a
  // different answer from "the content does not name this file", and the one
  // that makes a removal worth confirming.
  content_scanned: boolean;
}

// ReferencingAsset names an asset that references a resource.
export interface ReferencingAsset {
  id: string;
  name: string;
  owner_email?: string;
  // public marks an asset carrying an active link share: the reference makes
  // the file readable by anyone holding that link.
  public: boolean;
}

export interface ReferencingAssetsResponse {
  data: ReferencingAsset[];
  total: number;
  // hidden counts referencing assets this reader may not open. They are not
  // named, but they are counted: a delete would break them too.
  hidden: number;
  // truncated says the answer was cut at the server's bound rather than being
  // the whole of what references this file.
  truncated?: boolean;
}

const refsKey = (assetId: string) => ["portal", "asset-resource-refs", assetId];
const usedByKey = (resourceId: string) => ["portal", "resource-assets", resourceId];

// useAssetResources lists an asset's referenced resources. Disabled without an
// id so a viewer that has not resolved its asset can still call it.
export function useAssetResources(assetId: string | undefined) {
  return useQuery({
    queryKey: refsKey(assetId ?? ""),
    enabled: Boolean(assetId),
    queryFn: () =>
      apiFetch<AssetResourceRefsResponse>(`/assets/${assetId}/resources`),
  });
}

// useAddAssetResource references one more resource from an asset. The server
// checks the caller's own read permission on the file and refuses a resource
// they cannot read as not found.
export function useAddAssetResource(assetId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (resourceId: string) =>
      apiFetch<AssetResourceRefsResponse>(`/assets/${assetId}/resources`, {
        method: "POST",
        body: JSON.stringify({ resource_id: resourceId }),
      }),
    onSuccess: (_data, resourceId) => {
      void queryClient.invalidateQueries({ queryKey: refsKey(assetId) });
      // The resource's own "used by" list has just gained this asset.
      void queryClient.invalidateQueries({ queryKey: usedByKey(resourceId) });
    },
  });
}

export function useRemoveAssetResource(assetId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (resourceId: string) =>
      apiFetch<AssetResourceRefsResponse>(
        `/assets/${assetId}/resources/${resourceId}`,
        { method: "DELETE" },
      ),
    onSuccess: (_data, resourceId) => {
      void queryClient.invalidateQueries({ queryKey: refsKey(assetId) });
      void queryClient.invalidateQueries({ queryKey: usedByKey(resourceId) });
    },
  });
}

// useAssetsUsingResource answers "what is holding this file up?" for the
// resource detail view and its delete confirmation.
export function useAssetsUsingResource(resourceId: string | undefined) {
  return useQuery({
    queryKey: usedByKey(resourceId ?? ""),
    enabled: Boolean(resourceId),
    queryFn: () =>
      apiFetch<ReferencingAssetsResponse>(`/resources/${resourceId}/assets`),
  });
}
