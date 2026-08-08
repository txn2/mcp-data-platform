import { useId, useState } from "react";
import { MessageSquarePlus, X } from "lucide-react";
import { useCreateThread, type CreateThreadInput } from "@/api/portal/hooks";
import type {
  FeedbackTarget,
  ThreadKind,
  TextQuoteAnchor,
} from "@/api/portal/types";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { MentionTextarea } from "./MentionTextarea";
import { THREAD_KINDS } from "./meta";

interface Props {
  target: FeedbackTarget;
  availableAnchor: TextQuoteAnchor | null;
  onCancel: () => void;
  onCreated: (threadId: string) => void;
}

// Text-quote anchoring is offered for asset, prompt, and knowledge-page targets
// (all render their content through the anchorable markdown/plain-text
// renderers) when the reader has a live selection. Collections and standalone
// feedback are object-level and have nothing to anchor to.
const ANCHORABLE: FeedbackTarget["type"][] = ["asset", "prompt", "knowledge_page"];

function targetFields(target: FeedbackTarget): Partial<CreateThreadInput> {
  switch (target.type) {
    case "asset":
      return { target_type: "asset", asset_id: target.id, target_version: target.version };
    case "collection":
      return { target_type: "collection", collection_id: target.id };
    case "prompt":
      return { target_type: "prompt", prompt_id: target.id };
    case "knowledge_page":
      return { target_type: "knowledge_page", knowledge_page_id: target.id };
    case "standalone":
      return { target_type: "standalone" };
  }
}

// AnchorToggle offers to pin the thread to the passage the reader has selected,
// showing which passage that is so the choice is not made blind.
function AnchorToggle({
  checked,
  onChange,
  exact,
}: {
  checked: boolean;
  onChange: (checked: boolean) => void;
  exact: string;
}) {
  return (
    <Label className="items-start gap-2 rounded-md border bg-muted/40 p-2 text-xs font-normal">
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5"
      />
      <span className="min-w-0">
        <span className="font-medium">Anchor to selection</span>
        <span className="mt-0.5 block truncate text-muted-foreground italic">
          &ldquo;{exact}&rdquo;
        </span>
      </span>
    </Label>
  );
}

export function NewThreadForm({ target, availableAnchor, onCancel, onCreated }: Props) {
  const [kind, setKind] = useState<ThreadKind>("comment");
  const [body, setBody] = useState("");
  const [title, setTitle] = useState("");
  const [requiresResolution, setRequiresResolution] = useState(false);
  const [rating, setRating] = useState(5);
  const [useAnchor, setUseAnchor] = useState(true);
  const create = useCreateThread();
  const ids = useId();

  const anchor = ANCHORABLE.includes(target.type) ? availableAnchor : null;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const input: CreateThreadInput = {
      kind,
      body: body.trim(),
      requires_resolution: requiresResolution,
      ...(title.trim() ? { title: title.trim() } : {}),
      ...(kind === "rating" ? { rating } : {}),
      ...(anchor && useAnchor ? { anchor } : {}),
      ...targetFields(target),
    } as CreateThreadInput;

    create.mutate(input, {
      onSuccess: (thread) => onCreated(thread.id),
    });
  };

  return (
    <form onSubmit={submit} className="flex flex-col gap-3 p-4">
      <div className="flex items-center justify-between">
        <h3 className="flex items-center gap-1.5 text-sm font-semibold">
          <MessageSquarePlus className="h-4 w-4" /> New feedback
        </h3>
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={onCancel}
          aria-label="Cancel"
        >
          <X />
        </Button>
      </div>

      <div className="space-y-1">
        <Label className="text-xs text-muted-foreground">Kind</Label>
        <Select value={kind} onValueChange={(v) => setKind(v as ThreadKind)}>
          <SelectTrigger aria-label="Kind" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {THREAD_KINDS.map((k) => (
              <SelectItem key={k.value} value={k.value}>
                {k.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </div>

      {kind === "rating" && (
        <div className="space-y-1">
          <Label htmlFor={`${ids}-rating`} className="text-xs text-muted-foreground">
            Rating (1-5)
          </Label>
          <Input
            id={`${ids}-rating`}
            type="number"
            min={1}
            max={5}
            value={rating}
            onChange={(e) => setRating(Number(e.target.value))}
          />
        </div>
      )}

      <div className="space-y-1">
        <Label htmlFor={`${ids}-title`} className="text-xs text-muted-foreground">
          Title (optional)
        </Label>
        <Input
          id={`${ids}-title`}
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Short summary"
        />
      </div>

      <div className="space-y-1">
        <Label className="text-xs text-muted-foreground">Message</Label>
        <MentionTextarea
          target={target}
          value={body}
          onChange={setBody}
          required
          rows={4}
          placeholder="Describe your feedback. Type @ to mention someone."
          aria-label="Message"
          className="resize-y"
        />
      </div>

      {anchor && (
        <AnchorToggle checked={useAnchor} onChange={setUseAnchor} exact={anchor.exact} />
      )}

      <Label className="gap-2 text-xs">
        <input
          type="checkbox"
          checked={requiresResolution}
          onChange={(e) => setRequiresResolution(e.target.checked)}
        />
        Requires resolution
      </Label>

      {create.isError && (
        <p className="text-xs text-destructive">
          {(create.error as Error)?.message ?? "Failed to create feedback."}
        </p>
      )}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={!body.trim() || create.isPending}>
          {create.isPending ? "Posting…" : "Post feedback"}
        </Button>
      </div>
    </form>
  );
}
