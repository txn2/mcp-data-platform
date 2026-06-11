import { MessageCircle } from "lucide-react";
import { FeedbackPanel } from "@/components/feedback/FeedbackPanel";

// FeedbackChannelPage is the standalone feedback channel: feedback not tied to
// a single asset, collection, or prompt. Every authenticated user can read and
// post here, so it doubles as a shared suggestion box. It reuses the same
// FeedbackPanel as the per-object drawer, rendered full-page without a close.
export function FeedbackChannelPage() {
  return (
    <div className="mx-auto flex h-full max-w-3xl flex-col p-4">
      <div className="mb-4">
        <h1 className="flex items-center gap-2 text-xl font-semibold">
          <MessageCircle className="h-5 w-5" /> Feedback
        </h1>
        <p className="text-sm text-muted-foreground">
          General feedback and suggestions, visible to everyone on the platform.
          Feedback on a specific asset, collection, or prompt lives on that item.
        </p>
      </div>
      <div className="min-h-0 flex-1 overflow-hidden rounded-lg border">
        <FeedbackPanel target={{ type: "standalone" }} canModerate={false} />
      </div>
    </div>
  );
}
