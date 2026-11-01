import { useRef, useState, type ReactNode } from "react";
import { Building2, Globe, Hash, Lock, Send } from "lucide-react";
import { useShareToChannel, type PortalChannel } from "@/api/portal/hooks";
import { usePostResourceToChannel } from "@/api/resources/shares";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectGroup, SelectItem, SelectLabel, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { LIFETIMES } from "./model";
import { compareNames } from "@/lib/sortByName";

/** Who can open a posted link: an asset's two modes, a file's two. */
type PostAccess = "authenticated" | "public" | "existing";

/** One place a post can go. */
export interface Destination {
  key: string;
  channel: string;
  /** The bot's chat channel id; empty posts to the channel's own. */
  target?: string;
  /** "#general" or the channel's own name. */
  label: string;
  /** "Slack", "Mattermost", "Email". */
  kind: string;
  /** Readers of this destination see the preview image. */
  showsFiles: boolean;
  /** Why it cannot be posted to, when its chat channels could not be read. */
  unavailable?: string;
}

const FILE_KINDS = new Set(["slack", "mattermost", "email"]);

/** kindLabel names a channel's kind the way a person would. */
export function kindLabel(kind: string): string {
  const known: Record<string, string> = { slack: "Slack", mattermost: "Mattermost", email: "Email", webhook: "Webhook" };
  return known[kind] ?? kind.charAt(0).toUpperCase() + kind.slice(1);
}

/**
 * destinations flattens the channels into the places a post can go: every
 * chat channel a Slack or Mattermost bot is in, every other channel as
 * itself, and a bot whose chat channels could not be read, shown with why.
 * They are ordered by kind and then by label, the order the picker groups
 * them in (#2389).
 */
export function destinations(channels: PortalChannel[]): Destination[] {
  const out: Destination[] = [];
  for (const ch of channels) {
    const base = { channel: ch.name, kind: kindLabel(ch.kind), showsFiles: FILE_KINDS.has(ch.kind) };
    for (const t of ch.targets) out.push({ ...base, key: `${ch.name}\u0000${t.id}`, target: t.id, label: `#${t.name}` });
    if (ch.targets.length > 0) continue;
    const bot = ch.kind === "slack" || ch.kind === "mattermost";
    if (bot && ch.targets_error) {
      out.push({ ...base, key: ch.name, label: ch.name, unavailable: ch.targets_error });
      continue;
    }
    if (bot && !ch.target) continue;
    out.push({ ...base, key: ch.name, label: ch.kind === "email" ? ch.name : `#${ch.name}` });
  }
  return out.sort((a, b) => compareNames(a.kind, b.kind) || compareNames(a.label, b.label));
}

/** The item a post shows. */
export interface PostItem {
  type: "asset" | "resource";
  id: string;
  name: string;
  thumbnail?: string;
  sharer: string;
}

/**
 * ChannelView posts the item to a channel (#2241, #2291): where, who can open
 * the link it carries, a message, and a preview of what the channel will see.
 * A post that went out returns to the Share view; one refused keeps the form.
 */
export function ChannelView({
  item,
  options,
  onBack,
  onPosted,
}: {
  item: PostItem;
  options: Destination[];
  onBack: () => void;
  onPosted: (label: string) => void;
}) {
  const isFile = item.type === "resource";
  const [key, setKey] = useState("");
  const [access, setAccess] = useState<PostAccess>("authenticated");
  const [ttl, setTtl] = useState("168h");
  const [message, setMessage] = useState("");
  const chosen = options.find((o) => o.key === key);
  const { post, error, isPending } = useChannelPost(item, (label) => onPosted(label));

  return (
    <>
      <div className="space-y-[22px] px-6 pt-3 pb-[22px]">
        <Field label="Channel" htmlFor="post-channel">
          <ChannelPicker options={options} value={key} onChange={setKey} />
        </Field>
        <Field label="Who can open it" htmlFor="post-access">
          <AccessPicker access={access} onAccess={setAccess} isFile={isFile} />
          {access === "public" && (
            <div className="mt-2 flex items-center gap-2 text-sm">
              <span className="flex-1">Link stops working</span>
              <Select value={ttl} onValueChange={setTtl}>
                <SelectTrigger size="sm" aria-label="Link stops working">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {LIFETIMES.map((o) => (
                    <SelectItem key={o.value} value={o.value}>
                      {o.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}
          <p className="mt-1.5 text-xs text-muted-foreground">
            {access === "public"
              ? "Anyone in the channel, and anyone they forward it to, can open it until the link stops working."
              : "Channel members open it after signing in. Remove the post from the Share screen to stop the link working."}
          </p>
        </Field>
        <Field label="Message (optional)" htmlFor="post-message">
          <Textarea
            id="post-message"
            rows={3}
            placeholder="Say something about it"
            value={message}
            onChange={(e) => setMessage(e.target.value)}
            className="field-sizing-fixed min-h-0 rounded-[10px] text-sm"
          />
        </Field>
        <div>
          <div className="mb-1.5 text-[13px] font-semibold text-foreground/80">The channel will see</div>
          <Preview item={item} message={message} />
          {chosen?.showsFiles && !isFile && (
            <p className="mt-1.5 text-xs text-muted-foreground">
              Everyone in this channel sees the preview image and, for a text item, the start of its content.
            </p>
          )}
        </div>
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </div>
      <div className="flex flex-wrap justify-end gap-2 border-t px-6 py-4">
        <Button variant="outline" className="h-10 rounded-[9px]" onClick={onBack}>
          Back
        </Button>
        <Button className="h-10 rounded-[9px]" disabled={!chosen || isPending} onClick={() => chosen && post(chosen, access, ttl, message)}>
          <Send />
          {isPending ? "Posting…" : "Post"}
        </Button>
      </div>
    </>
  );
}

function Field({ label, htmlFor, children }: { label: string; htmlFor: string; children: ReactNode }) {
  return (
    <div>
      <Label htmlFor={htmlFor} className="mb-1.5 text-[13px] font-semibold text-foreground/80">
        {label}
      </Label>
      {children}
    </div>
  );
}

function ChannelPicker({ options, value, onChange }: { options: Destination[]; value: string; onChange: (v: string) => void }) {
  const kinds = [...new Set(options.map((o) => o.kind))];
  const item = (o: Destination) => (
    <SelectItem key={o.key} value={o.key} disabled={!!o.unavailable}>
      <span className="flex flex-col">
        <span>
          {o.label} · {o.kind}
        </span>
        {o.unavailable && <span className="text-xs text-muted-foreground">{o.unavailable}</span>}
      </span>
    </SelectItem>
  );
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger id="post-channel" className="h-11 w-full rounded-[10px]" aria-label="Channel">
        <Hash className="size-[18px] text-muted-foreground" />
        <SelectValue placeholder="Choose where to post it" />
      </SelectTrigger>
      <SelectContent>
        {kinds.length > 1
          ? kinds.map((k) => (
              <SelectGroup key={k}>
                <SelectLabel>{k}</SelectLabel>
                {options.filter((o) => o.kind === k).map(item)}
              </SelectGroup>
            ))
          : options.map(item)}
      </SelectContent>
    </Select>
  );
}

function AccessPicker({ access, onAccess, isFile }: { access: PostAccess; onAccess: (v: PostAccess) => void; isFile: boolean }) {
  const Icon = access === "public" ? Globe : access === "existing" ? Lock : Building2;
  return (
    <Select value={access} onValueChange={(v) => onAccess(v as PostAccess)}>
      <SelectTrigger id="post-access" className="h-11 w-full rounded-[10px]" aria-label="Who can open it">
        <Icon className="size-[18px] text-muted-foreground" />
        <SelectValue />
      </SelectTrigger>
      <SelectContent>
        <SelectItem value="authenticated">Anyone who can sign in</SelectItem>
        {isFile ? (
          <SelectItem value="existing">Only people with access</SelectItem>
        ) : (
          <SelectItem value="public">Anyone with the link</SelectItem>
        )}
      </SelectContent>
    </Select>
  );
}

function Preview({ item, message }: { item: PostItem; message: string }) {
  return (
    <div className="rounded-xl border p-3.5 text-sm" data-testid="post-preview">
      {message.trim() && <p className="mb-2 whitespace-pre-wrap">{message.trim()}</p>}
      <div className="flex gap-3 border-l-[3px] pl-3">
        {item.thumbnail ? (
          <img src={item.thumbnail} alt="" className="h-[60px] w-24 shrink-0 rounded object-cover" />
        ) : (
          <div className="grid h-[60px] w-24 shrink-0 place-items-center rounded bg-muted text-muted-foreground">
            <Hash className="size-5" />
          </div>
        )}
        <div className="min-w-0">
          <div className="truncate font-semibold text-primary">{item.name}</div>
          <div className="text-xs text-muted-foreground">Shared by {item.sharer} · Open in the portal</div>
        </div>
      </div>
    </div>
  );
}

/**
 * useChannelPost sends one post and says what went wrong. A second click while
 * a post is on its way is ignored.
 */
function useChannelPost(item: PostItem, onPosted: (label: string) => void) {
  const share = useShareToChannel();
  const shareFile = usePostResourceToChannel();
  const [error, setError] = useState("");
  const sending = useRef(false);

  function post(to: Destination, access: PostAccess, ttl: string, message: string) {
    if (sending.current) return;
    sending.current = true;
    setError("");
    const handlers = {
      onSuccess: (res: { suppressed?: boolean; queued?: number; detail?: string }) => {
        if (!res.suppressed && (res.queued ?? 0) > 0) onPosted(to.label);
        else setError(res.detail ?? "The post was not sent.");
      },
      onError: (err: unknown) => setError(err instanceof Error ? err.message : "The post failed."),
      onSettled: () => {
        sending.current = false;
      },
    };
    const where = { channel: to.channel, target: to.target, message: message.trim() };
    if (item.type === "resource") {
      shareFile.mutate({ resourceId: item.id, ...where, access_mode: access === "existing" ? "existing" : "authenticated" }, handlers);
      return;
    }
    share.mutate(
      {
        assetId: item.id,
        ...where,
        access_mode: access === "public" ? "public" : "authenticated",
        expires_in: access === "public" ? ttl : undefined,
      },
      handlers,
    );
  }

  return { post, error, isPending: share.isPending || shareFile.isPending };
}
