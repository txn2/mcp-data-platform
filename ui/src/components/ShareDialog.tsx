import { useState } from "react";
import * as Dialog from "@radix-ui/react-dialog";
import { X, Copy, Link, Trash2, Check } from "lucide-react";
import { useShares, useCreateShare, useRevokeShare } from "@/api/portal/hooks";

interface Props {
  assetId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function ShareDialog({ assetId, open, onOpenChange }: Props) {
  const { data: shares = [] } = useShares(assetId);
  const createShare = useCreateShare();
  const revokeShare = useRevokeShare();
  const [ttl, setTtl] = useState("24h");
  const [userId, setUserId] = useState("");
  const [copied, setCopied] = useState<string | null>(null);

  function handleCreatePublicLink() {
    createShare.mutate({ assetId, expires_in: ttl });
  }

  function handleShareWithUser() {
    if (!userId.trim()) return;
    createShare.mutate({ assetId, shared_with_user_id: userId.trim() });
    setUserId("");
  }

  function handleCopy(text: string, id: string) {
    navigator.clipboard.writeText(text).then(() => {
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
          </div>

          {/* Share with user */}
          <div className="mb-4">
            <h3 className="text-sm font-medium mb-2">Share with User</h3>
            <div className="flex gap-2">
              <input
                type="text"
                placeholder="User ID"
                value={userId}
                onChange={(e) => setUserId(e.target.value)}
                className="flex-1 rounded-md border bg-background px-3 py-1.5 text-sm outline-none ring-ring focus:ring-2"
              />
              <button
                type="button"
                onClick={handleShareWithUser}
                disabled={!userId.trim() || createShare.isPending}
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
                      {share.shared_with_user_id ? (
                        <span className="text-muted-foreground">
                          User: {share.shared_with_user_id}
                        </span>
                      ) : (
                        <span className="font-mono text-xs text-muted-foreground truncate block">
                          {share.token.slice(0, 16)}...
                        </span>
                      )}
                      <span className="text-xs text-muted-foreground ml-2">
                        ({share.access_count} views)
                      </span>
                    </div>
                    <div className="flex items-center gap-1 ml-2">
                      {!share.shared_with_user_id && (
                        <button
                          type="button"
                          onClick={() =>
                            handleCopy(
                              `${window.location.origin}/portal/view/${share.token}`,
                              share.id,
                            )
                          }
                          className="rounded p-1 hover:bg-accent"
                          title="Copy link"
                        >
                          {copied === share.id ? (
                            <Check className="h-3.5 w-3.5 text-green-500" />
                          ) : (
                            <Copy className="h-3.5 w-3.5" />
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
