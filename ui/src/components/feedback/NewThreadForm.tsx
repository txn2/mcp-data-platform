import { useState } from "react";
import { MessageSquarePlus, X } from "lucide-react";
import { useCreateThread, type CreateThreadInput } from "@/api/portal/hooks";
import type {
  FeedbackTarget,
  ThreadKind,
  TextQuoteAnchor,
} from "@/api/portal/types";
import { THREAD_KINDS } from "./meta";

interface Props {
  target: FeedbackTarget;
  availableAnchor: TextQuoteAnchor | null;
  onCancel: () => void;
  onCreated: (threadId: string) => void;
}

function targetFields(target: FeedbackTarget): Partial<CreateThreadInput> {
  switch (target.type) {
    case "asset":
      return { target_type: "asset", asset_id: target.id, target_version: target.version };
    case "collection":
      return { target_type: "collection", collection_id: target.id };
    case "prompt":
      return { target_type: "prompt", prompt_id: target.id };
    case "standalone":
      return { target_type: "standalone" };
  }
}

export function NewThreadForm({ target, availableAnchor, onCancel, onCreated }: Props) {
  const [kind, setKind] = useState<ThreadKind>("comment");
  const [body, setBody] = useState("");
  const [title, setTitle] = useState("");
  const [requiresResolution, setRequiresResolution] = useState(false);
  const [rating, setRating] = useState(5);
  const [useAnchor, setUseAnchor] = useState(true);
  const create = useCreateThread();

  // Text-quote anchoring is offered for asset targets when the user has a live
  // selection inside the content. Other targets are object-level in this view.
  const canAnchor = target.type === "asset" && !!availableAnchor;

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    const input: CreateThreadInput = {
      kind,
      body: body.trim(),
      requires_resolution: requiresResolution,
      ...(title.trim() ? { title: title.trim() } : {}),
      ...(kind === "rating" ? { rating } : {}),
      ...(canAnchor && useAnchor && availableAnchor ? { anchor: availableAnchor } : {}),
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
        <button
          type="button"
          onClick={onCancel}
          className="rounded p-1 hover:bg-accent"
          aria-label="Cancel"
        >
          <X className="h-4 w-4" />
        </button>
      </div>

      <label className="text-xs font-medium text-muted-foreground">
        Kind
        <select
          value={kind}
          onChange={(e) => setKind(e.target.value as ThreadKind)}
          className="mt-1 w-full rounded-md border bg-background px-2 py-1.5 text-sm"
        >
          {THREAD_KINDS.map((k) => (
            <option key={k.value} value={k.value}>
              {k.label}
            </option>
          ))}
        </select>
      </label>

      {kind === "rating" && (
        <label className="text-xs font-medium text-muted-foreground">
          Rating (1-5)
          <input
            type="number"
            min={1}
            max={5}
            value={rating}
            onChange={(e) => setRating(Number(e.target.value))}
            className="mt-1 w-full rounded-md border bg-background px-2 py-1.5 text-sm"
          />
        </label>
      )}

      <label className="text-xs font-medium text-muted-foreground">
        Title (optional)
        <input
          value={title}
          onChange={(e) => setTitle(e.target.value)}
          placeholder="Short summary"
          className="mt-1 w-full rounded-md border bg-background px-2 py-1.5 text-sm"
        />
      </label>

      <label className="text-xs font-medium text-muted-foreground">
        Message
        <textarea
          value={body}
          onChange={(e) => setBody(e.target.value)}
          required
          rows={4}
          placeholder="Describe your feedback"
          className="mt-1 w-full resize-y rounded-md border bg-background px-2 py-1.5 text-sm"
        />
      </label>

      {canAnchor && (
        <label className="flex items-start gap-2 rounded-md border bg-muted/40 p-2 text-xs">
          <input
            type="checkbox"
            checked={useAnchor}
            onChange={(e) => setUseAnchor(e.target.checked)}
            className="mt-0.5"
          />
          <span className="min-w-0">
            <span className="font-medium">Anchor to selection</span>
            <span className="mt-0.5 block truncate text-muted-foreground italic">
              &ldquo;{availableAnchor?.exact}&rdquo;
            </span>
          </span>
        </label>
      )}

      <label className="flex items-center gap-2 text-xs font-medium">
        <input
          type="checkbox"
          checked={requiresResolution}
          onChange={(e) => setRequiresResolution(e.target.checked)}
        />
        Requires resolution
      </label>

      {create.isError && (
        <p className="text-xs text-destructive">
          {(create.error as Error)?.message ?? "Failed to create feedback."}
        </p>
      )}

      <div className="flex justify-end gap-2">
        <button
          type="button"
          onClick={onCancel}
          className="rounded-md border px-3 py-1.5 text-sm hover:bg-accent"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={!body.trim() || create.isPending}
          className="rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
        >
          {create.isPending ? "Posting…" : "Post feedback"}
        </button>
      </div>
    </form>
  );
}
