import { Link, ChevronDown, ChevronRight, TriangleAlert } from "lucide-react";
import type { SharePermission, ShareAccessMode } from "@/api/portal/types";
import { UserPicker } from "@/components/UserPicker";

/**
 * The two ways to create a share, split out of ShareDialog so the dialog file
 * stays about the dialog: mint a link addressed to nobody, or address one
 * person.
 */

/**
 * LinkAccessMode is the subset of share access modes a link share can take:
 * a link addressed to nobody cannot be restricted to a recipient.
 */
export type LinkAccessMode = Exclude<ShareAccessMode, "restricted">;

export interface LinkShareSectionProps {
  linkAccess: LinkAccessMode;
  setLinkAccess: (v: LinkAccessMode) => void;
  ttl: string;
  setTtl: (v: string) => void;
  showOptions: boolean;
  setShowOptions: (fn: (v: boolean) => boolean) => void;
  showExpiration: boolean;
  setShowExpiration: (v: boolean) => void;
  noticeText: string;
  setNoticeText: (v: string) => void;
  onCreate: () => void;
  isPending: boolean;
}

/**
 * LinkShareSection creates a share that is not addressed to a person: either
 * one any signed-in user can open, or a public one that opens without signing
 * in. The public choice carries an explicit warning, since it is the only mode
 * where possession of the URL is the whole of the access check.
 */
export function LinkShareSection({
  linkAccess,
  setLinkAccess,
  ttl,
  setTtl,
  showOptions,
  setShowOptions,
  showExpiration,
  setShowExpiration,
  noticeText,
  setNoticeText,
  onCreate,
  isPending,
}: LinkShareSectionProps) {
  return (
    <div className="mb-4">
      <h3 className="text-sm font-medium mb-2">Share by Link</h3>
      <div className="flex gap-2">
        <select
          value={linkAccess}
          onChange={(e) => setLinkAccess(e.target.value as LinkAccessMode)}
          className="rounded-md border bg-background px-3 py-1.5 text-sm"
          aria-label="Who can open this link"
        >
          <option value="authenticated">Signed-in users</option>
          <option value="public">Anyone with the link</option>
        </select>
        <select
          value={ttl}
          onChange={(e) => setTtl(e.target.value)}
          className="rounded-md border bg-background px-3 py-1.5 text-sm"
          aria-label="Link expiration"
        >
          <option value="1h">1 hour</option>
          <option value="24h">24 hours</option>
          <option value="168h">7 days</option>
          <option value="720h">30 days</option>
        </select>
        <button
          type="button"
          onClick={onCreate}
          disabled={isPending}
          className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          <Link className="h-3.5 w-3.5" />
          Create Link
        </button>
      </div>
      {linkAccess === "public" && (
        <p className="mt-2 flex items-start gap-1.5 text-xs text-amber-700 dark:text-amber-400">
          <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>
            This link works without signing in. Anyone who receives it,
            including anyone it is forwarded to, can open the content.
          </span>
        </p>
      )}
      <button
        type="button"
        onClick={() => setShowOptions((v) => !v)}
        className="flex items-center gap-1 mt-2 text-xs text-muted-foreground hover:text-foreground transition-colors"
      >
        {showOptions ? (
          <ChevronDown className="h-3 w-3" />
        ) : (
          <ChevronRight className="h-3 w-3" />
        )}
        Options
      </button>
      {showOptions && (
        <div className="mt-2 space-y-2 rounded-md border bg-muted/30 p-3">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={showExpiration}
              onChange={(e) => setShowExpiration(e.target.checked)}
              className="rounded border-input"
            />
            Show expiration notice
          </label>
          <div>
            <label
              className="text-sm text-muted-foreground"
              htmlFor="notice-text"
            >
              Notice text
            </label>
            <input
              id="notice-text"
              type="text"
              placeholder="Leave empty to hide the notice"
              value={noticeText}
              onChange={(e) => setNoticeText(e.target.value)}
              className="mt-1 w-full rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
            />
            <p className="mt-1 text-xs text-muted-foreground">
              Clear to hide notice bar entirely.
            </p>
          </div>
        </div>
      )}
    </div>
  );
}

/** MAX_SHARE_MESSAGE mirrors the server's cap on the sharer's note. */
const MAX_SHARE_MESSAGE = 500;

export interface UserShareSectionProps {
  email: string;
  setEmail: (v: string) => void;
  permission: SharePermission;
  setPermission: (v: SharePermission) => void;
  notify: boolean;
  setNotify: (v: boolean) => void;
  message: string;
  setMessage: (v: string) => void;
  error: string | null;
  onClearError: () => void;
  onShare: () => void;
  isPending: boolean;
}

/**
 * UserShareSection creates a share addressed to one person. Such a share has
 * no expiration: it grants that person access until the owner revokes it,
 * which is why this section offers no lifetime control the way the link
 * section does.
 *
 * The email and the note are the only things that reach the recipient, so
 * both live here together: notification on by default, and the note shown
 * only while it has somewhere to go.
 */
export function UserShareSection({
  email,
  setEmail,
  permission,
  setPermission,
  notify,
  setNotify,
  message,
  setMessage,
  error,
  onClearError,
  onShare,
  isPending,
}: UserShareSectionProps) {
  const hasRecipient = email.trim() !== "";
  return (
    <div className="mb-4">
      <h3 className="text-sm font-medium mb-2">Share with User</h3>
      <p className="mb-2 text-xs text-muted-foreground">
        Only this person can open the link, and their access lasts until you
        revoke it. With an account they sign in and get the permission you
        grant; without one they can view (not edit) through single-use links
        emailed to them.
      </p>
      <div className="flex gap-2">
        <UserPicker
          value={email}
          onChange={(v) => {
            // Clear a rejection as soon as the sharer starts correcting it,
            // rather than leaving it under a field that no longer matches.
            onClearError();
            setEmail(v);
          }}
        />
        <select
          value={permission}
          onChange={(e) => setPermission(e.target.value as SharePermission)}
          className="rounded-md border bg-background px-3 py-1.5 text-sm"
          aria-label="Permission"
        >
          <option value="viewer">Viewer</option>
          <option value="editor">Editor</option>
        </select>
        <button
          type="button"
          onClick={onShare}
          disabled={!hasRecipient || isPending}
          className="rounded-md bg-secondary px-3 py-1.5 text-sm font-medium text-secondary-foreground hover:bg-secondary/80 disabled:opacity-50"
        >
          Share
        </button>
      </div>

      {/* The notice and note only matter once someone is named: a link share
          addressed to nobody has no recipient to mail. */}
      {hasRecipient && (
        <div className="mt-2 space-y-2">
          <label className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={notify}
              onChange={(e) => setNotify(e.target.checked)}
              className="rounded border-input"
            />
            Notify by email
          </label>
          {notify && (
            <div>
              <label
                className="text-xs text-muted-foreground"
                htmlFor="share-message"
              >
                Message (optional)
              </label>
              <textarea
                id="share-message"
                rows={2}
                maxLength={MAX_SHARE_MESSAGE}
                placeholder="Here's the Q3 revenue breakdown you asked about"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                className="mt-1 w-full resize-y rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
              />
              <p className="mt-1 text-xs text-muted-foreground">
                Plain text only, included in the email. Links and formatting are
                not accepted. {MAX_SHARE_MESSAGE - message.length} characters
                left.
              </p>
            </div>
          )}
        </div>
      )}

      {error && (
        <p
          role="alert"
          className="mt-2 flex items-start gap-1.5 text-xs text-destructive"
        >
          <TriangleAlert className="mt-0.5 h-3.5 w-3.5 shrink-0" />
          <span>{error}</span>
        </p>
      )}
    </div>
  );
}
