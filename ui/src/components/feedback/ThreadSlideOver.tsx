import { ExternalLink } from "lucide-react";
import { useThread } from "@/api/portal/hooks";
import { ThreadDetail } from "./ThreadDetail";
import { SlideOver } from "./SlideOver";
import { targetMeta } from "./targetRoute";

interface Props {
  threadId: string;
  onClose: () => void;
  onNavigate: (path: string) => void;
}

// ThreadSlideOver shows a thread's full detail in a right-side panel on the
// Feedback page, with a link back to the asset, collection, or prompt it lives
// on. Moderation controls stay backend-gated: the thread's author and admins
// still see them (ThreadDetail derives that), everyone else uses "Go to item".
export function ThreadSlideOver({ threadId, onClose, onNavigate }: Props) {
  const { data: thread } = useThread(threadId);
  const meta = thread ? targetMeta(thread) : null;

  return (
    <SlideOver onClose={onClose}>
      {meta?.route && (
        <div className="border-b p-2">
          <button
            type="button"
            onClick={() => {
              onNavigate(meta.route!);
              onClose();
            }}
            className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          >
            <ExternalLink className="h-3.5 w-3.5" />
            Go to {meta.label.toLowerCase()}
          </button>
        </div>
      )}
      <div className="min-h-0 flex-1 overflow-auto">
        <ThreadDetail threadId={threadId} canModerate={false} onBack={onClose} onDeleted={onClose} />
      </div>
    </SlideOver>
  );
}
