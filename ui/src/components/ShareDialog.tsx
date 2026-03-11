import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Link, Trash2, Check, Copy, ChevronDown, ChevronRight } from "lucide-react";
import { useShares, useCreateShare, useRevokeShare } from "@/api/portal/hooks";

interface Props {
  assetId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

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

export function ShareDialog({ assetId, open, onOpenChange }: Props) {
  const { data: shares = [] } = useShares(assetId);
  const createShare = useCreateShare();
  const revokeShare = useRevokeShare();
  const [ttl, setTtl] = useState("24h");
  const [email, setEmail] = useState("");
  const [copied, setCopied] = useState<string | null>(null);
  const [showOptions, setShowOptions] = useState(false);
  const [showExpiration, setShowExpiration] = useState(true);
  const [noticeText, setNoticeText] = useState(
    "Proprietary & Confidential. Only share with authorized viewers.",
  );

  function handleCreatePublicLink() {
    createShare.mutate({
      assetId,
      expires_in: ttl,
      ...(!showExpiration && { hide_expiration: true }),
      notice_text: noticeText.trim(),
    });
  }

  function handleShareWithUser() {
    if (!email.trim()) return;
    createShare.mutate({ assetId, shared_with_email: email.trim() });
    setEmail("");
  }

  function handleCopy(text: string, id: string) {
    navigator.clipboard.writeText(text).then(() => {
      setCopied(id);
      setTimeout(() => setCopied(null), 2000);
    }).catch(() => {
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
    <Dialog.Root open={open} onOpenChange={onOpenChange}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 bg-black/40 z-40" />
        <Dialog.Content className="fixed top-1/2 left-1/2 z-50 -translate-x-1/2 -translate-y-1/2 w-full max-w-lg rounded-lg border bg-card p-6 shadow-lg">
          <div className="flex items-center justify-between mb-4">
            <Dialog.Title className="text-lg font-semibold">Share Asset</Dialog.Title>
            <Dialog.Close className="rounded-md p-1 hover:bg-accent">
              <X className="h-4 w-4" />
            </Dialog.Close>
          </div>

          {/* Create public link */}
          <div className="mb-4">
            <h3 className="text-sm font-medium mb-2">Public Link</h3>
            <div className="flex gap-2">
              <select
                value={ttl}
                onChange={(e) => setTtl(e.target.value)}
                className="rounded-md border bg-background px-3 py-1.5 text-sm"
              >
                <option value="1h">1 hour</option>
                <option value="24h">24 hours</option>
                <option value="168h">7 days</option>
                <option value="720h">30 days</option>
              </select>
              <button
                type="button"
                onClick={handleCreatePublicLink}
                disabled={createShare.isPending}
                className="flex items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
              >
                <Link className="h-3.5 w-3.5" />
                Create Link
              </button>
            </div>
            <button
              type="button"
              onClick={() => setShowOptions((v) => !v)}
              className="flex items-center gap-1 mt-2 text-xs text-muted-foreground hover:text-foreground transition-colors"
            >
              {showOptions ? <ChevronDown className="h-3 w-3" /> : <ChevronRight className="h-3 w-3" />}
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
                  <label className="text-sm text-muted-foreground" htmlFor="notice-text">
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

          {/* Share with user */}
          <div className="mb-4">
            <h3 className="text-sm font-medium mb-2">Share with User</h3>
            <div className="flex gap-2">
              <input
                type="email"
                placeholder="Email address"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                className="flex-1 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
              />
              <button
                type="button"
                onClick={handleShareWithUser}
                disabled={!email.trim() || createShare.isPending}
                className="rounded-md bg-secondary px-3 py-1.5 text-sm font-medium text-secondary-foreground hover:bg-secondary/80 disabled:opacity-50"
              >
                Share
              </button>
            </div>
          </div>

          {/* Active shares */}
          {activeShares.length > 0 && (
            <div>
              <h3 className="text-sm font-medium mb-2">Active Shares ({activeShares.length})</h3>
              <div className="space-y-2 max-h-48 overflow-auto">
                {activeShares.map((share) => (
                  <div
                    key={share.id}
                    className="flex items-center justify-between rounded-md border px-3 py-2 text-sm"
                  >
                    <div className="min-w-0 flex-1">
                      {share.shared_with_user_id || share.shared_with_email ? (
                        <span className="text-muted-foreground">
                          User: {share.shared_with_email || share.shared_with_user_id}
                        </span>
                      ) : (
                        <span className="text-muted-foreground">
                          Public Link
                        </span>
                      )}
                      <span className="text-xs text-muted-foreground ml-2">
                        ({share.access_count} views)
                      </span>
                      <span className="text-xs text-muted-foreground ml-2">
                        {formatTimeRemaining(share.expires_at)}
                      </span>
                    </div>
                    <div className="flex items-center gap-1 ml-2">
                      {!share.shared_with_user_id && !share.shared_with_email && (
                        <button
                          type="button"
                          onClick={() =>
                            handleCopy(
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
                      <button
                        type="button"
                        onClick={() => revokeShare.mutate(share.id)}
                        className="rounded p-1 hover:bg-destructive/10 text-destructive"
                        title="Revoke"
                      >
                        <Trash2 className="h-3.5 w-3.5" />
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
