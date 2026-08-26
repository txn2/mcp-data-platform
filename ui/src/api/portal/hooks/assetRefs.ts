import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import { apiFetch } from "../client";

// The things an asset's content references (#1475, #1488), from both ends: the
// asset's own list, and the target's answer to what is holding it up.
//
// An asset names a managed resource by its mcp:// URI and another asset by its
// mcp:asset:<id> reference, and the platform rewrites that string as it serves
// (#1474). These hooks manage the declaration behind that rewrite; they never
// touch the asset's content, which is why the panel has to hand the author the
// reference to paste.

// RefTargetKind is what a reference points at.
export type RefTargetKind = "resource" | "asset";

// RefTarget names one target: the kind, and the id in that kind's id space.
// Both are needed to address a reference, because a resource id and an asset id
// are indistinguishable strings.
export interface RefTarget {
  kind: RefTargetKind;
  id: string;
}

// AssetRef is one reference as the sidebar renders it.
export interface AssetRef {
  target_kind: RefTargetKind;
  target_id: string;
  uri: string;
  position: number;
  declared_by?: string;
  display_name?: string;
  description?: string;
  mime_type?: string;
  size_bytes?: number;
  // Resource-only: the file's own name, its category, and the scope it is
  // filed under.
  filename?: string;
  category?: string;
  scope?: "global" | "persona" | "user";
  scope_id?: string;
  // owner_email names who owns a referenced asset -- the asset's counterpart of
  // a resource's scope.
  owner_email?: string;
  // content_url is the reference's own serving URL, the same one the platform
  // writes into the content this reader is served. The panel loads a thumbnail
  // through it rather than through the target's own route, which a reader of a
  // shared asset may not be allowed to call.
  content_url?: string;
  // broken marks a reference whose target was deleted. The row survives so the
  // owner can see that the asset is serving without it.
  broken?: boolean;
  // readable is whether this reader could open the target on its own, as
  // opposed to through the asset. It decides whether the row is a link: a
  // reader of a shared asset can see a target they have no direct access to.
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
// gives the referenced target.
export interface RefAudience {
  public: boolean;
  shared_with_users: boolean;
}

export interface AssetRefsResponse {
  data: AssetRef[];
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
  // different answer from "the content does not name this", and the one that
  // makes a removal worth confirming.
  content_scanned: boolean;
}

// ReferencingAsset names an asset that references a target.
export interface ReferencingAsset {
  id: string;
  name: string;
  owner_email?: string;
  // public marks an asset carrying an active link share: the reference makes
  // the target readable by anyone holding that link.
  public: boolean;
}

export interface ReferencingAssetsResponse {
  data: ReferencingAsset[];
  total: number;
  // hidden counts referencing assets this reader may not open. They are not
  // named, but they are counted: a delete would break them too.
  hidden: number;
  // truncated says the answer was cut at the server's bound rather than being
  // the whole of what references this target.
  truncated?: boolean;
}

const refsKey = (assetId: string) => ["portal", "asset-refs", assetId];
const usedByKey = (kind: RefTargetKind, id: string) => ["portal", "used-by", kind, id];

// usedByPath is the route that answers "what is holding this up?" for either
// kind. One path shape for both keeps the two sections asking the same
// question.
const usedByPath = (kind: RefTargetKind, id: string) =>
  kind === "asset" ? `/assets/${id}/used-by` : `/resources/${id}/used-by`;

// useAssetRefs lists what an asset's content references. Disabled without an id
// so a viewer that has not resolved its asset can still call it.
export function useAssetRefs(assetId: string | undefined) {
  return useQuery({
    queryKey: refsKey(assetId ?? ""),
    enabled: Boolean(assetId),
    queryFn: () => apiFetch<AssetRefsResponse>(`/assets/${assetId}/references`),
  });
}

// useAddAssetRef references one more target from an asset. The server checks
// the caller's own read permission on the target and refuses one they cannot
// read as not found.
export function useAddAssetRef(assetId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (target: RefTarget) =>
      apiFetch<AssetRefsResponse>(`/assets/${assetId}/references`, {
        method: "POST",
        body: JSON.stringify({ target_kind: target.kind, target_id: target.id }),
      }),
    onSuccess: (_data, target) => {
      void queryClient.invalidateQueries({ queryKey: refsKey(assetId) });
      // The target's own "used by" list has just gained this asset.
      void queryClient.invalidateQueries({ queryKey: usedByKey(target.kind, target.id) });
    },
  });
}

export function useRemoveAssetRef(assetId: string) {
  const queryClient = useQueryClient();
  return useMutation({
    mutationFn: (target: RefTarget) =>
      apiFetch<AssetRefsResponse>(
        `/assets/${assetId}/references/${target.kind}/${target.id}`,
        { method: "DELETE" },
      ),
    onSuccess: (_data, target) => {
      void queryClient.invalidateQueries({ queryKey: refsKey(assetId) });
      void queryClient.invalidateQueries({ queryKey: usedByKey(target.kind, target.id) });
    },
  });
}

// useAssetsUsingTarget answers "what is holding this up?" for a resource's or an
// asset's detail view, and for the delete confirmation in front of either.
export function useAssetsUsingTarget(kind: RefTargetKind, id: string | undefined) {
  return useQuery({
    queryKey: usedByKey(kind, id ?? ""),
    enabled: Boolean(id),
    queryFn: () => apiFetch<ReferencingAssetsResponse>(usedByPath(kind, id ?? "")),
  });
}
