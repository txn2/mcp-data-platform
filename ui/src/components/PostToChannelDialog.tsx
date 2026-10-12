import { useRef, useState } from "react";
import { Send } from "lucide-react";
import { usePortalChannels, usePublishToChannel, type PortalChannel } from "@/api/portal/hooks";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";

// PostToChannelDialog posts an asset to a notification channel (#1723): the
// asset's name, its content, its preview image and a link to it, through the
// same notify tool an agent asking for the post would call. The channels
// offered are the ones the signed-in person reaches.
//
// A post that was accepted replaces the form with the answer and leaves only
// Close (#2091): every click on Post is another message in the channel, so
// posting the asset again takes reopening the dialog. A second click while a
// post is on its way is ignored.
export function PostToChannelDialog({
  assetId,
  open,
  onOpenChange,
}: {
  assetId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}) {
  const { data, isLoading, error: loadError } = usePortalChannels(open);
  const publish = usePublishToChannel();
  const [channel, setChannel] = useState("");
  const [message, setMessage] = useState("");
  const [outcome, setOutcome] = useState<{ ok: boolean; detail: string } | null>(null);
  // Set from the click that starts a post until it is answered: two clicks
  // can land before the pending state re-renders the button disabled.
  const sending = useRef(false);

  const channels = data?.channels ?? [];
  const posted = outcome?.ok === true;

  function close(next: boolean) {
    if (!next) {
      setOutcome(null);
      setMessage("");
      publish.reset();
    }
    onOpenChange(next);
  }

  function submit() {
    if (sending.current) return;
    sending.current = true;
    setOutcome(null);
    publish.mutate(
      { channel, assetId, message },
      {
        onSuccess: (res) => setOutcome({ ok: !res.suppressed && res.queued > 0, detail: res.detail }),
        onError: (err) => setOutcome({ ok: false, detail: err instanceof Error ? err.message : "The post failed" }),
        onSettled: () => {
          sending.current = false;
        },
      },
    );
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Post to a channel</DialogTitle>
          <DialogDescription>
            The channel receives this asset's name, its content, its preview and a link back to it.
          </DialogDescription>
        </DialogHeader>
        {!posted && (
          <DialogBody
            isLoading={isLoading}
            loadFailed={Boolean(loadError)}
            note={data?.note}
            channels={channels}
            channel={channel}
            onChannel={setChannel}
            message={message}
            onMessage={setMessage}
          />
        )}
        {outcome && <Outcome ok={outcome.ok} detail={outcome.detail} />}
        <Footer
          posted={posted}
          pending={publish.isPending}
          canPost={Boolean(channel) && channels.length > 0}
          onClose={() => close(false)}
          onPost={submit}
        />
      </DialogContent>
    </Dialog>
  );
}

// Footer is Close, and Post until a post is accepted.
function Footer({
  posted,
  pending,
  canPost,
  onClose,
  onPost,
}: {
  posted: boolean;
  pending: boolean;
  canPost: boolean;
  onClose: () => void;
  onPost: () => void;
}) {
  return (
    <DialogFooter>
      <Button variant="outline" onClick={onClose}>
        Close
      </Button>
      {!posted && (
        <Button onClick={onPost} disabled={!canPost || pending}>
          <Send />
          {pending ? "Posting…" : "Post"}
        </Button>
      )}
    </DialogFooter>
  );
}

// DialogBody is what the dialog shows above its buttons: the channels to
// choose from and the message, or why there is nothing to choose.
function DialogBody({
  isLoading,
  loadFailed,
  note,
  channels,
  channel,
  onChannel,
  message,
  onMessage,
}: {
  isLoading: boolean;
  loadFailed: boolean;
  note?: string;
  channels: PortalChannel[];
  channel: string;
  onChannel: (v: string) => void;
  message: string;
  onMessage: (v: string) => void;
}) {
  if (isLoading) return <p className="text-sm text-muted-foreground">Loading channels…</p>;
  if (loadFailed) {
    return <p className="text-sm text-destructive">Could not read the channels you can post to.</p>;
  }
  if (channels.length === 0) {
    return (
      <p className="text-sm text-muted-foreground" data-testid="no-channels">
        {note ?? "You cannot post to any channel."}
      </p>
    );
  }
  const chosen = channels.find((c) => c.name === channel);
  return (
    <div className="space-y-4">
      <div className="space-y-1.5">
        <Label className="text-xs" htmlFor="post-channel">
          Channel
        </Label>
        <Select value={channel} onValueChange={onChannel}>
          <SelectTrigger id="post-channel" aria-label="Channel">
            <SelectValue placeholder="Choose a channel" />
          </SelectTrigger>
          <SelectContent>
            {channels.map((c) => (
              <SelectItem key={c.name} value={c.name}>
                {c.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {chosen && (
          <p className="text-xs text-muted-foreground">
            {chosen.description || `Shows ${chosen.carries}.`}
          </p>
        )}
      </div>
      <div className="space-y-1.5">
        <Label className="text-xs" htmlFor="post-message">
          Message
        </Label>
        <Textarea
          id="post-message"
          value={message}
          onChange={(e) => onMessage(e.target.value)}
          placeholder="Optional line posted above the asset"
          rows={3}
        />
      </div>
    </div>
  );
}

// Outcome is what the tool answered the post with, in its own words.
function Outcome({ ok, detail }: { ok: boolean; detail: string }) {
  return (
    <p role="status" className={ok ? "text-sm text-emerald-700 dark:text-emerald-400" : "text-sm text-destructive"}>
      {detail}
    </p>
  );
}
