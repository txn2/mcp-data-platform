import { Link, ChevronDown, ChevronRight, TriangleAlert } from "lucide-react";
import type { SharePermission, ShareAccessMode } from "@/api/portal/types";
import { UserPicker } from "@/components/UserPicker";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

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

/** The lifetimes a link share can be minted with. */
const TTL_OPTIONS = [
  { value: "1h", label: "1 hour" },
  { value: "24h", label: "24 hours" },
  { value: "168h", label: "7 days" },
  { value: "720h", label: "30 days" },
];

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
    <div>
      <h3 className="text-sm font-medium mb-2">Share by Link</h3>
      <div className="flex gap-2">
        <Select
          value={linkAccess}
          onValueChange={(v) => setLinkAccess(v as LinkAccessMode)}
        >
          <SelectTrigger size="sm" aria-label="Who can open this link">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="authenticated">Signed-in users</SelectItem>
            <SelectItem value="public">Anyone with the link</SelectItem>
          </SelectContent>
        </Select>
        <Select value={ttl} onValueChange={setTtl}>
          <SelectTrigger size="sm" aria-label="Link expiration">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {TTL_OPTIONS.map((o) => (
              <SelectItem key={o.value} value={o.value}>
                {o.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        <Button type="button" size="sm" onClick={onCreate} disabled={isPending}>
          <Link />
          Create Link
        </Button>
      </div>
      {linkAccess === "public" && (
        <Alert variant="warning" className="mt-2">
          <TriangleAlert />
          <AlertDescription className="text-xs">
            This link works without signing in. Anyone who receives it,
            including anyone it is forwarded to, can open the content.
          </AlertDescription>
        </Alert>
      )}
      <Button
        type="button"
        variant="ghost"
        size="xs"
        onClick={() => setShowOptions((v) => !v)}
        className="mt-2 px-1 text-muted-foreground"
      >
        {showOptions ? <ChevronDown /> : <ChevronRight />}
        Options
      </Button>
      {showOptions && (
        <div className="mt-2 space-y-2 rounded-md border bg-muted/30 p-3">
          <Label className="text-sm font-normal">
            <input
              type="checkbox"
              checked={showExpiration}
              onChange={(e) => setShowExpiration(e.target.checked)}
              className="rounded border-input"
            />
            Show expiration notice
          </Label>
          <div>
            <Label
              className="font-normal text-muted-foreground"
              htmlFor="notice-text"
            >
              Notice text
            </Label>
            <Input
              id="notice-text"
              type="text"
              placeholder="Leave empty to hide the notice"
              value={noticeText}
              onChange={(e) => setNoticeText(e.target.value)}
              className="mt-1 text-sm"
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
    <div>
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
        <Select
          value={permission}
          onValueChange={(v) => setPermission(v as SharePermission)}
        >
          <SelectTrigger size="sm" aria-label="Permission">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="viewer">Viewer</SelectItem>
            <SelectItem value="editor">Editor</SelectItem>
          </SelectContent>
        </Select>
        <Button
          type="button"
          variant="secondary"
          size="sm"
          onClick={onShare}
          disabled={!hasRecipient || isPending}
        >
          Share
        </Button>
      </div>

      {/* The notice and note only matter once someone is named: a link share
          addressed to nobody has no recipient to mail. */}
      {hasRecipient && (
        <div className="mt-2 space-y-2">
          <Label className="text-sm font-normal">
            <input
              type="checkbox"
              checked={notify}
              onChange={(e) => setNotify(e.target.checked)}
              className="rounded border-input"
            />
            Notify by email
          </Label>
          {notify && (
            <div>
              <Label
                className="text-xs font-normal text-muted-foreground"
                htmlFor="share-message"
              >
                Message (optional)
              </Label>
              <Textarea
                id="share-message"
                rows={2}
                maxLength={MAX_SHARE_MESSAGE}
                placeholder="Here's the Q3 revenue breakdown you asked about"
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                // field-sizing-fixed: the primitive sizes to its content by
                // default, which silently overrides the two rows asked for.
                className="mt-1 field-sizing-fixed min-h-0 resize-y text-sm"
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
        <Alert variant="destructive" className="mt-2">
          <TriangleAlert />
          <AlertDescription className="text-xs">{error}</AlertDescription>
        </Alert>
      )}
    </div>
  );
}
