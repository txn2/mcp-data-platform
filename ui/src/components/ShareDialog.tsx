import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  useShares,
  useCreateShare,
  useRevokeShare,
  useCollectionShares,
  useCreateCollectionShare,
  usePromptShares,
  useCreatePromptShare,
} from "@/api/portal/hooks";
import type { SharePermission } from "@/api/portal/types";
import { parseEmailAddress } from "@/lib/emailAddress";
import { ActiveShares } from "@/components/share/ActiveShares";
import {
  LinkShareSection,
  UserShareSection,
  type LinkAccessMode,
} from "@/components/share/ShareSections";

export type ShareTarget =
  | { type: "asset"; id: string }
  | { type: "collection"; id: string }
  | { type: "prompt"; id: string };

interface Props {
  /** @deprecated Use `target` instead. */
  assetId?: string;
  target?: ShareTarget;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ShareDialog({ assetId, target, open, onOpenChange }: Props) {
  // Resolve target: prefer `target` prop, fall back to `assetId` for backward compat.
  const resolved: ShareTarget = target ?? { type: "asset", id: assetId ?? "" };
  const isCollection = resolved.type === "collection";
  const isPrompt = resolved.type === "prompt";

  const { data: assetShares = [] } = useShares(
    resolved.type === "asset" ? resolved.id : "",
  );
  const { data: collectionShares = [] } = useCollectionShares(
    isCollection ? resolved.id : "",
  );
  const { data: promptShares = [] } = usePromptShares(
    isPrompt ? resolved.id : "",
  );
  const shares = isCollection
    ? collectionShares
    : isPrompt
      ? promptShares
      : assetShares;

  const createAssetShare = useCreateShare();
  const createCollShare = useCreateCollectionShare();
  const createPromptShare = useCreatePromptShare();
  const revokeShare = useRevokeShare();
  const [ttl, setTtl] = useState("24h");
  const [email, setEmail] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const [confirmRevoke, setConfirmRevoke] = useState<string | null>(null);
  const [permission, setPermission] = useState<SharePermission>("viewer");
  const [notify, setNotify] = useState(true);
  const [message, setMessage] = useState("");
  const [shareError, setShareError] = useState<string | null>(null);
  const [linkAccess, setLinkAccess] = useState<LinkAccessMode>("authenticated");
  const [showOptions, setShowOptions] = useState(false);
  const [showExpiration, setShowExpiration] = useState(true);
  const [noticeText, setNoticeText] = useState(
    "Proprietary & Confidential. Only share with authorized viewers.",
  );

  const isPending =
    createAssetShare.isPending ||
    createCollShare.isPending ||
    createPromptShare.isPending;

  function handleCreateLink() {
    const opts = {
      expires_in: ttl,
      ...(!showExpiration && { hide_expiration: true }),
      notice_text: noticeText.trim(),
      access_mode: linkAccess,
    };
    if (isCollection) {
      createCollShare.mutate({ collectionId: resolved.id, ...opts });
    } else {
      createAssetShare.mutate({ assetId: resolved.id, ...opts });
    }
  }

  // Recipient shares are restricted: only the named person (and the sender)
  // can open the link, whether or not they receive it by email. They carry no
  // expiration -- access ends when the owner revokes the share.
  function handleShareWithUser() {
    if (!email.trim()) return;
    // Normalize here too, not only on the field's blur: a click on Share
    // straight from the input can beat the blur handler, and the address that
    // reaches the server decides who the share matches at view time.
    const recipient = parseEmailAddress(email);
    if (!recipient) {
      setShareError("Enter a single email address, e.g. user@example.com.");
      return;
    }
    setShareError(null);

    const opts = {
      shared_with_email: recipient,
      permission,
      // Only stated when it differs from the default, so the request says
      // what the sharer changed rather than restating the default.
      ...(!notify && { notify: false }),
      ...(notify && message.trim() !== "" && { message: message.trim() }),
    };
    const onError = (err: unknown) =>
      setShareError(
        err instanceof Error ? err.message : "Failed to create the share.",
      );

    if (isPrompt) {
      createPromptShare.mutate({ promptId: resolved.id, ...opts }, { onError });
    } else if (isCollection) {
      createCollShare.mutate(
        { collectionId: resolved.id, ...opts },
        { onError },
      );
    } else {
      createAssetShare.mutate({ assetId: resolved.id, ...opts }, { onError });
    }
    setEmail("");
    setMessage("");
  }

  function handleCopy(text: string, id: string) {
    navigator.clipboard
      .writeText(text)
      .then(() => {
        setCopied(id);
        setTimeout(() => setCopied(null), 2000);
      })
      .catch(() => {
        // Fallback: select a temporary input for manual copy.
        const el = document.createElement("textarea");
        el.value = text;
        document.body.appendChild(el);
        el.select();
        document.execCommand("copy");
        document.body.removeChild(el);
        setCopied(id);
        setTimeout(() => setCopied(null), 2000);
      });
  }

  const activeShares = shares.filter((s) => !s.revoked);

  return (
    <Dialog
      open={open}
      onOpenChange={(v) => {
        if (!v) setConfirmRevoke(null);
        onOpenChange(v);
      }}
    >
      <DialogContent aria-describedby={undefined}>
        <DialogHeader>
          <DialogTitle className="text-lg">
            {isPrompt
              ? "Share Prompt"
              : isCollection
                ? "Share Collection"
                : "Share Asset"}
          </DialogTitle>
        </DialogHeader>

        {/* Link shares are not offered for prompts, which are run, not viewed via a public page */}
        {!isPrompt && (
          <LinkShareSection
            linkAccess={linkAccess}
            setLinkAccess={setLinkAccess}
            ttl={ttl}
            setTtl={setTtl}
            showOptions={showOptions}
            setShowOptions={setShowOptions}
            showExpiration={showExpiration}
            setShowExpiration={setShowExpiration}
            noticeText={noticeText}
            setNoticeText={setNoticeText}
            onCreate={handleCreateLink}
            isPending={isPending}
          />
        )}

        {/* Share with user */}
        <UserShareSection
          email={email}
          setEmail={setEmail}
          permission={permission}
          setPermission={setPermission}
          notify={notify}
          setNotify={setNotify}
          message={message}
          setMessage={setMessage}
          error={shareError}
          onClearError={() => setShareError(null)}
          onShare={handleShareWithUser}
          isPending={isPending}
        />

        <ActiveShares
          shares={activeShares}
          copied={copied}
          confirmRevoke={confirmRevoke}
          setConfirmRevoke={setConfirmRevoke}
          onCopy={handleCopy}
          onRevoke={(id) => revokeShare.mutate(id)}
        />
      </DialogContent>
    </Dialog>
  );
}
