import { Trash2, Check, Copy } from "lucide-react";
import type { Share } from "@/api/portal/types";

function formatTimeRemaining(expiresAt?: string): string {
  if (!expiresAt) return "No expiration";
  const remaining = new Date(expiresAt).getTime() - Date.now();
  if (remaining <= 0) return "Expired";
  const hours = Math.floor(remaining / 3600000);
  if (hours < 1) {
    const minutes = Math.max(1, Math.floor(remaining / 60000));
    return `Expires in ${minutes}m`;
  }
  if (hours < 24) return `Expires in ${hours}h`;
  const days = Math.floor(hours / 24);
  return `Expires in ${days}d`;
}

/**
 * ActiveShares lists the shares already granted on an item, with the copy and
 * revoke actions for each. A share addressed to a person reads "No
 * expiration", which is the whole truth for it: such a share ends when it is
 * revoked, and the revoke control next to it is how.
 */
export function ActiveShares({
  shares,
  copied,
  confirmRevoke,
  setConfirmRevoke,
  onCopy,
  onRevoke,
}: {
  shares: Share[];
  copied: string | null;
  confirmRevoke: string | null;
  setConfirmRevoke: (id: string | null) => void;
  onCopy: (text: string, id: string) => void;
  onRevoke: (id: string) => void;
}) {
  if (shares.length === 0) return null;
  return (
    <div>
      <h3 className="text-sm font-medium mb-2">
        Active Shares ({shares.length})
      </h3>
      <div className="space-y-2 max-h-48 overflow-auto">
        {shares.map((share) => (
          <ShareRow
            key={share.id}
            share={share}
            copied={copied}
            confirmRevoke={confirmRevoke}
            setConfirmRevoke={setConfirmRevoke}
            onCopy={onCopy}
            onRevoke={onRevoke}
          />
        ))}
      </div>
    </div>
  );
}

/**
 * ShareRow is one granted share: who it is for, how long it lasts, and the
 * copy and revoke actions. Split out of the list because a row branches on
 * recipient kind, access mode, copy state and revoke confirmation, which is
 * more decision than belongs in a map callback.
 */
function ShareRow({
  share,
  copied,
  confirmRevoke,
  setConfirmRevoke,
  onCopy,
  onRevoke,
}: {
  share: Share;
  copied: string | null;
  confirmRevoke: string | null;
  setConfirmRevoke: (id: string | null) => void;
  onCopy: (text: string, id: string) => void;
  onRevoke: (id: string) => void;
}) {
  return (
    <div className="flex items-center justify-between rounded-md border px-3 py-2 text-sm">
      <ShareDescriptor share={share} />
      <ShareActions
        share={share}
        copied={copied}
        confirmRevoke={confirmRevoke}
        setConfirmRevoke={setConfirmRevoke}
        onCopy={onCopy}
        onRevoke={onRevoke}
      />
    </div>
  );
}

/** ShareDescriptor names who a share is for and how long it lasts. */
function ShareDescriptor({ share }: { share: Share }) {
  // A share names a person or it names nobody; the two read differently
  // enough to be separate lines rather than one nested ternary.
  const named = Boolean(share.shared_with_user_id || share.shared_with_email);
  return (
    <div className="min-w-0 flex-1">
      {named ? <RecipientLabel share={share} /> : <LinkLabel share={share} />}
      {!named && share.access_count > 0 && (
        <span className="text-xs text-muted-foreground ml-2">
          ({share.access_count} {share.access_count === 1 ? "view" : "views"})
        </span>
      )}
      <span className="text-xs text-muted-foreground ml-2">
        {formatTimeRemaining(share.expires_at)}
      </span>
    </div>
  );
}

/** RecipientLabel describes a share addressed to a person. */
function RecipientLabel({ share }: { share: Share }) {
  const editor = share.permission === "editor";
  return (
    <span className="text-muted-foreground">
      User: {share.shared_with_email || share.shared_with_user_id}
      <span
        className={`ml-1.5 text-xs px-1.5 py-0.5 rounded-full font-medium ${editor ? "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300" : "bg-gray-100 text-gray-600 dark:bg-gray-800 dark:text-gray-400"}`}
      >
        {editor ? "Editor" : "Viewer"}
      </span>
      {share.access_mode === "public" && (
        <span className="ml-1.5 rounded-full bg-amber-100 px-1.5 py-0.5 text-xs font-medium text-amber-800 dark:bg-amber-950 dark:text-amber-300">
          Opens without sign-in
        </span>
      )}
    </span>
  );
}

/** LinkLabel describes a share addressed to nobody. */
function LinkLabel({ share }: { share: Share }) {
  return (
    <span className="text-muted-foreground">
      {share.access_mode === "public"
        ? "Link: anyone"
        : "Link: signed-in users"}
    </span>
  );
}

/** ShareActions is the copy-link and revoke control pair for one share. */
function ShareActions({
  share,
  copied,
  confirmRevoke,
  setConfirmRevoke,
  onCopy,
  onRevoke,
}: {
  share: Share;
  copied: string | null;
  confirmRevoke: string | null;
  setConfirmRevoke: (id: string | null) => void;
  onCopy: (text: string, id: string) => void;
  onRevoke: (id: string) => void;
}) {
  // Only a link share has a URL worth copying: a share addressed to a person
  // resolves for that person alone, so handing its URL around achieves
  // nothing.
  const isLink = !share.shared_with_user_id && !share.shared_with_email;
  return (
    <div className="flex items-center gap-1 ml-2">
      {isLink && (
        <button
          type="button"
          onClick={() =>
            onCopy(
              `${window.location.origin}/portal/view/${share.token}`,
              share.id,
            )
          }
          className="flex items-center gap-1 rounded px-2 py-1 text-xs hover:bg-accent"
          title="Copy public link"
        >
          {copied === share.id ? (
            <>
              <Check className="h-3.5 w-3.5 text-green-500" />
              Copied
            </>
          ) : (
            <>
              <Copy className="h-3.5 w-3.5" />
              Copy Link
            </>
          )}
        </button>
      )}
      {confirmRevoke === share.id ? (
        <div className="flex items-center gap-1">
          <button
            type="button"
            onClick={() => {
              onRevoke(share.id);
              setConfirmRevoke(null);
            }}
            className="rounded px-2 py-0.5 text-xs font-medium bg-destructive text-destructive-foreground hover:bg-destructive/90"
          >
            Remove
          </button>
          <button
            type="button"
            onClick={() => setConfirmRevoke(null)}
            className="rounded px-2 py-0.5 text-xs text-muted-foreground hover:text-foreground"
          >
            Cancel
          </button>
        </div>
      ) : (
        <button
          type="button"
          onClick={() => setConfirmRevoke(share.id)}
          className="rounded p-1 hover:bg-destructive/10 text-destructive"
          title="Revoke"
        >
          <Trash2 className="h-3.5 w-3.5" />
        </button>
      )}
    </div>
  );
}
