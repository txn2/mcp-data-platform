import { useRef, useState } from "react";
import { Send, TriangleAlert } from "lucide-react";
import {
  usePortalChannels,
  useShareToChannel,
  type PortalChannel,
} from "@/api/portal/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { usePostResourceToChannel } from "@/api/resources/shares";
import { TTL_OPTIONS, type LinkAccessMode } from "./ShareSections";

/**
 * ChannelTarget is what is posted: an asset, or a file (#2258), whose post
 * carries its page's link and no preview, and has no public link.
 */
export interface ChannelTarget {
  type: "asset" | "resource";
  id: string;
}

/** Who can open a posted link: an asset's modes, and a file's. */
type PostAccess = LinkAccessMode | "existing";

/** Destination is one place a channel share can go. */
interface Destination {
  /** key identifies the destination in the picker. */
  key: string;
  channel: string;
  /** target is the bot's chat channel id; empty posts to the channel's own. */
  target?: string;
  label: string;
  /** showsFiles is a destination whose readers see the preview image. */
  showsFiles: boolean;
}

/** Outcome is what the last post answered, in words for the sharer. */
interface Outcome {
  ok: boolean;
  detail: string;
}

/** The kinds that carry the preview image and the content excerpt as files. */
const FILE_KINDS = new Set(["slack", "mattermost", "email"]);

/**
 * destinations flattens the channels into the places a post can go: every
 * chat channel a Slack or Mattermost bot is in, and every other channel as
 * itself. A bot channel whose chat channels could not be read still offers
 * its own target when it has one.
 */
export function destinations(channels: PortalChannel[]): Destination[] {
  const out: Destination[] = [];
  for (const ch of channels) {
    const showsFiles = FILE_KINDS.has(ch.kind);
    if (ch.targets.length > 0) {
      for (const t of ch.targets) {
        out.push({
          key: `${ch.name}\u0000${t.id}`,
          channel: ch.name,
          target: t.id,
          label: `#${t.name} (${ch.name})`,
          showsFiles,
        });
      }
      continue;
    }
    if ((ch.kind === "slack" || ch.kind === "mattermost") && !ch.target) continue;
    out.push({
      key: ch.name,
      channel: ch.name,
      label: ch.kind === "email" ? `${ch.name} (email)` : ch.name,
      showsFiles,
    });
  }
  return out;
}

/**
 * ChannelShareSection shares an asset to a channel (#2241): it creates a link
 * share in the access mode chosen and posts the asset with that link, so the
 * channel's readers can open what they are shown. The channels offered are the
 * chat channels each Slack or Mattermost bot is in and the channels an
 * administrator set up, read each time the dialog opens.
 *
 * A post that was accepted clears the choice: every click on Post is another
 * message in the channel, so posting again takes choosing again (#2091).
 */
export function ChannelShareSection({ target, open }: { target: ChannelTarget; open: boolean }) {
  const { data, isLoading, error } = usePortalChannels(open);
  const channels = data?.channels ?? [];
  const options = destinations(channels);
  const unreadable = channels.filter((c) => c.targets_error);

  if (isLoading) {
    return <p className="text-xs text-muted-foreground">Loading channels…</p>;
  }
  if (error) {
    return <p className="text-xs text-destructive">Could not read the channels you can share to.</p>;
  }
  if (options.length === 0 && unreadable.length === 0) return null;
  return <ChannelShareForm target={target} options={options} unreadable={unreadable} />;
}

/** ChannelShareForm is the section's controls once there is somewhere to post. */
function ChannelShareForm({
  target,
  options,
  unreadable,
}: {
  target: ChannelTarget;
  options: Destination[];
  unreadable: PortalChannel[];
}) {
  const isFile = target.type === "resource";
  const [key, setKey] = useState("");
  const [access, setAccess] = useState<PostAccess>("authenticated");
  const [ttl, setTtl] = useState("168h");
  const [message, setMessage] = useState("");
  const chosen = options.find((o) => o.key === key);
  const { post, outcome, clearOutcome, isPending } = useChannelPost(target, () => {
    setKey("");
    setMessage("");
  });

  return (
    <div>
      <h3 className="text-sm font-medium mb-2">Share to a Channel</h3>
      <div className="space-y-2">
        <DestinationPicker
          options={options}
          value={key}
          onChange={(v) => {
            setKey(v);
            clearOutcome();
          }}
        />
        {unreadable.map((c) => (
          <p key={c.name} className="text-xs text-muted-foreground">
            The chat channels of {c.name} could not be read: {c.targets_error}
          </p>
        ))}
        <AccessPicker access={access} onAccess={setAccess} ttl={ttl} onTtl={setTtl} isFile={isFile} />
        <Label className="sr-only" htmlFor="channel-share-message">
          Message
        </Label>
        <Textarea
          id="channel-share-message"
          value={message}
          onChange={(e) => setMessage(e.target.value)}
          placeholder={isFile ? "Optional message posted above the file" : "Optional message posted above the asset"}
          rows={2}
          className="text-sm"
        />
        {chosen?.showsFiles && !isFile && (
          <Alert variant="warning">
            <TriangleAlert />
            <AlertDescription className="text-xs">
              Everyone in this channel sees the asset's preview image and, for a
              text asset, the first part of its content.
            </AlertDescription>
          </Alert>
        )}
        <div className="flex items-center justify-between gap-2">
          <OutcomeLine outcome={outcome} />
          <Button
            type="button"
            size="sm"
            onClick={() => chosen && post(chosen, access, ttl, message)}
            disabled={!chosen || isPending}
          >
            <Send />
            {isPending ? "Posting…" : "Post"}
          </Button>
        </div>
      </div>
    </div>
  );
}

/**
 * useChannelPost sends one channel share and keeps what it answered. A second
 * click while a post is on its way is ignored; onPosted runs after a post
 * that went through.
 */
function useChannelPost(target: ChannelTarget, onPosted: () => void) {
  const share = useShareToChannel();
  const shareFile = usePostResourceToChannel();
  const [outcome, setOutcome] = useState<Outcome | null>(null);
  // Set from the click that starts a post until it is answered: two clicks
  // can land before the pending state re-renders the button disabled.
  const sending = useRef(false);

  function post(to: Destination, access: PostAccess, ttl: string, message: string) {
    if (sending.current) return;
    sending.current = true;
    setOutcome(null);
    const handlers = {
      onSuccess: (res: { suppressed?: boolean; queued?: number; detail?: string }) => {
        const ok = !res.suppressed && (res.queued ?? 0) > 0;
        setOutcome({ ok, detail: ok ? `Posted to ${to.label}.` : (res.detail ?? "The post was not sent.") });
        if (ok) onPosted();
      },
      onError: (err: unknown) =>
        setOutcome({ ok: false, detail: err instanceof Error ? err.message : "The post failed." }),
      onSettled: () => {
        sending.current = false;
      },
    };
    if (target.type === "resource") {
      shareFile.mutate(
        {
          resourceId: target.id,
          channel: to.channel,
          target: to.target,
          access_mode: access === "existing" ? "existing" : "authenticated",
          message: message.trim(),
        },
        handlers,
      );
      return;
    }
    share.mutate(
      {
        assetId: target.id,
        channel: to.channel,
        target: to.target,
        access_mode: access === "public" ? "public" : "authenticated",
        expires_in: access === "public" ? ttl : undefined,
        message: message.trim(),
      },
      handlers,
    );
  }

  return {
    post,
    outcome,
    clearOutcome: () => setOutcome(null),
    isPending: share.isPending || shareFile.isPending,
  };
}

/** DestinationPicker chooses where the post goes. */
function DestinationPicker({
  options,
  value,
  onChange,
}: {
  options: Destination[];
  value: string;
  onChange: (v: string) => void;
}) {
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger size="sm" aria-label="Channel" className="w-full">
        <SelectValue placeholder="Choose a channel" />
      </SelectTrigger>
      <SelectContent>
        {options.map((o) => (
          <SelectItem key={o.key} value={o.key}>
            {o.label}
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

/** AccessPicker chooses who opens the posted link, and a public link's life. */
function AccessPicker({
  access,
  onAccess,
  ttl,
  onTtl,
  isFile,
}: {
  access: PostAccess;
  onAccess: (v: PostAccess) => void;
  ttl: string;
  onTtl: (v: string) => void;
  isFile: boolean;
}) {
  return (
    <div className="flex gap-2">
      <Select value={access} onValueChange={(v) => onAccess(v as PostAccess)}>
        <SelectTrigger size="sm" aria-label="Who can open the posted link">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="authenticated">Signed-in users</SelectItem>
          {isFile ? (
            <SelectItem value="existing">People who can already open it</SelectItem>
          ) : (
            <SelectItem value="public">Anyone with the link</SelectItem>
          )}
        </SelectContent>
      </Select>
      {access === "public" && (
        <Select value={ttl} onValueChange={onTtl}>
          <SelectTrigger size="sm" aria-label="Posted link expiration">
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
      )}
    </div>
  );
}

/** OutcomeLine says what the last post answered, or holds the row's place. */
function OutcomeLine({ outcome }: { outcome: Outcome | null }) {
  if (!outcome) return <span />;
  return (
    <p
      role="status"
      className={outcome.ok ? "text-xs text-emerald-700 dark:text-emerald-400" : "text-xs text-destructive"}
    >
      {outcome.detail}
    </p>
  );
}
