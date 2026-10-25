import {
  useCollectionShares,
  useCreateCollectionShare,
  useCreatePromptShare,
  useCreateShare,
  usePromptShares,
  useRemoveGeneralLink,
  useRevokeShare,
  useSetGeneralLink,
  useSetSharePermission,
  useShares,
  type GeneralLinkBody,
} from "@/api/portal/hooks";
import type { Share, SharePermission } from "@/api/portal/types";
import {
  useCreateResourceShare,
  useResourceShares,
  useRevokeResourceShare,
  useSetResourceSharePermission,
} from "@/api/resources/shares";

/** What a share dialog is opened on. */
export type ShareKind = "asset" | "collection" | "prompt" | "resource";

/** A grant to make: a person by address or a group, with what they can do. */
export interface GrantBody {
  shared_with_email?: string;
  shared_with_persona?: string;
  permission: SharePermission;
  notify: boolean;
  message?: string;
}

/**
 * useShareData is one item's shares and every change the dialog makes to them,
 * the same five calls whatever the item is (#2291). Each returns a promise so
 * the dialog can say what happened.
 */
export function useShareData(kind: ShareKind, id: string) {
  const asset = useShares(kind === "asset" ? id : "");
  const collection = useCollectionShares(kind === "collection" ? id : "");
  const prompt = usePromptShares(kind === "prompt" ? id : "");
  const file = useResourceShares(kind === "resource" ? id : "");
  const query = { asset, collection, prompt, resource: file }[kind];

  const createAsset = useCreateShare();
  const createCollection = useCreateCollectionShare();
  const createPrompt = useCreatePromptShare();
  const createFile = useCreateResourceShare();
  const revoke = useRevokeShare();
  const revokeFile = useRevokeResourceShare(kind === "resource" ? id : "");
  const setPermission = useSetSharePermission();
  const setFilePermission = useSetResourceSharePermission(kind === "resource" ? id : "");
  const setLink = useSetGeneralLink();
  const removeLink = useRemoveGeneralLink();

  function grant(body: GrantBody): Promise<unknown> {
    const opts = {
      ...(body.shared_with_email ? { shared_with_email: body.shared_with_email } : {}),
      ...(body.shared_with_persona ? { shared_with_persona: body.shared_with_persona } : {}),
      permission: body.permission,
      // Stated only when it differs from the default, as the old dialog did.
      ...(!body.notify && { notify: false }),
      ...(body.notify && body.message ? { message: body.message } : {}),
    };
    switch (kind) {
      case "resource":
        return createFile.mutateAsync({ resourceId: id, ...opts });
      case "prompt":
        return createPrompt.mutateAsync({ promptId: id, ...opts });
      case "collection":
        return createCollection.mutateAsync({ collectionId: id, ...opts });
      default:
        return createAsset.mutateAsync({ assetId: id, ...opts });
    }
  }

  return {
    shares: (query.data ?? []) as Share[],
    isLoading: query.isLoading,
    grant,
    revoke: (shareId: string): Promise<unknown> =>
      kind === "resource" ? revokeFile.mutateAsync(shareId) : revoke.mutateAsync(shareId),
    setPermission: (shareId: string, permission: SharePermission): Promise<unknown> =>
      kind === "resource"
        ? setFilePermission.mutateAsync({ shareId, permission })
        : setPermission.mutateAsync({ shareId, permission }),
    /** An asset's or a collection's general link; other items have none. */
    setLink: (body: GeneralLinkBody) =>
      setLink.mutateAsync({ target: { type: kind === "collection" ? "collection" : "asset", id }, ...body }),
    removeLink: () => removeLink.mutateAsync({ type: kind === "collection" ? "collection" : "asset", id }),
  };
}

export type ShareData = ReturnType<typeof useShareData>;
