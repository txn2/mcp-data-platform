import { useState } from "react";
import { Hash } from "lucide-react";
import type { PortalChannel } from "@/api/portal/hooks";
import type { Share } from "@/api/portal/types";
import { Avatar } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import { kindLabel } from "./ChannelView";
import { opened, shortDate } from "./model";

/**
 * postedWhere reads where a post went, as the server recorded it, against the
 * channels the dialog can see: an asset's post records "#general (team-chat)",
 * a file's "<chat channel id> on team-chat", and a channel with no chat
 * channels its own name. The kind comes from the channel of that name.
 */
export function postedWhere(postedTo: string, channels: PortalChannel[]): { channel: string; kind: string } {
  const asset = /^(#.+?)\s*\(([^)]+)\)$/.exec(postedTo);
  const file = /^(.+?) on (.+)$/.exec(postedTo);
  const ch = channels.find((c) => c.name === (asset?.[2] ?? file?.[2] ?? postedTo));
  return { channel: channelName(postedTo, asset?.[1], file?.[1], ch), kind: ch ? kindLabel(ch.kind) : "" };
}

/** channelName is the chat channel a post went to, or the channel it stands for. */
function channelName(postedTo: string, assetTarget: string | undefined, fileTarget: string | undefined, ch: PortalChannel | undefined): string {
  if (assetTarget) return assetTarget;
  if (fileTarget) {
    const t = ch?.targets.find((x) => x.id === fileTarget);
    return t ? `#${t.name}` : fileTarget;
  }
  return ch?.kind === "email" ? postedTo : `#${postedTo}`;
}

/**
 * PostedToChannels lists where the item was posted (#2291): each post's link
 * is a share of its own, and removing it, after a confirm on the row, stops
 * that link opening.
 */
export function PostedToChannels({
  posts,
  channels,
  onRemove,
}: {
  posts: Share[];
  channels: PortalChannel[];
  onRemove: (s: Share) => void;
}) {
  const [confirm, setConfirm] = useState<string | null>(null);
  if (posts.length === 0) return null;
  return (
    <section aria-labelledby="share-posts-label">
      <h3 id="share-posts-label" className="mb-1.5 text-[13px] font-semibold text-foreground/80">
        Posted to channels
      </h3>
      {posts.map((p) => {
        const where = postedWhere(p.posted_to ?? "", channels);
        const detail = [where.kind, `posted ${shortDate(p.created_at)}`, opened(p.access_count)].filter(Boolean).join(" · ");
        return (
          <div key={p.id} className="flex min-h-[52px] items-center gap-3" data-testid="posted-row">
            <Avatar tint="bg-muted text-foreground">
              <Hash />
            </Avatar>
            {confirm === p.id ? (
              <>
                <span className="flex-1 text-sm">The link in {where.channel} stops working.</span>
                <Button
                  variant="destructive"
                  size="sm"
                  onClick={() => {
                    setConfirm(null);
                    onRemove(p);
                  }}
                >
                  Remove
                </Button>
                <Button variant="ghost" size="sm" onClick={() => setConfirm(null)}>
                  Cancel
                </Button>
              </>
            ) : (
              <>
                <div className="min-w-0 flex-1">
                  <div className="truncate text-sm">{where.channel || "A channel"}</div>
                  <div className="mt-0.5 truncate text-xs text-muted-foreground">{detail}</div>
                </div>
                <Button variant="ghost" size="sm" onClick={() => setConfirm(p.id)}>
                  Remove
                </Button>
              </>
            )}
          </div>
        );
      })}
    </section>
  );
}
