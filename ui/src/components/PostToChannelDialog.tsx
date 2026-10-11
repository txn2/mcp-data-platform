import { useState } from "react";
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

  const channels = data?.channels ?? [];

  function close(next: boolean) {
    if (!next) {
      setOutcome(null);
      setMessage("");
      publish.reset();
    }
    onOpenChange(next);
  }

  function submit() {
    setOutcome(null);
    publish.mutate(
      { channel, assetId, message },
      {
        onSuccess: (res) => setOutcome({ ok: !res.suppressed && res.queued > 0, detail: res.detail }),
        onError: (err) => setOutcome({ ok: false, detail: err instanceof Error ? err.message : "The post failed" }),
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
        {outcome && <Outcome ok={outcome.ok} detail={outcome.detail} />}
        <DialogFooter>
          <Button variant="outline" onClick={() => close(false)}>
            Close
          </Button>
          <Button onClick={submit} disabled={!channel || publish.isPending || channels.length === 0}>
            <Send />
            {publish.isPending ? "Posting…" : "Post"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
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
