import { useState } from "react";
import { MessageCircle, Clock, Inbox, Megaphone, Plus } from "lucide-react";
import { cn } from "@/lib/utils";
import { usePractitionerWorklist, useSMEWorklist } from "@/api/portal/hooks";
import { ActivityFeed } from "@/components/feedback/ActivityFeed";
import { InboxPanel } from "@/components/feedback/InboxPanel";
import { FeedbackPanel } from "@/components/feedback/FeedbackPanel";
import { ThreadSlideOver } from "@/components/feedback/ThreadSlideOver";
import { SlideOver } from "@/components/feedback/SlideOver";
import { NewThreadForm } from "@/components/feedback/NewThreadForm";

interface Props {
  onNavigate: (path: string) => void;
}

type Tab = "recent" | "worklist" | "general";

// FeedbackPage is the portal's feedback hub (#617). It flows full-width with the
// rest of the portal and gathers the three feedback surfaces under one roof:
//   - Recent: every thread on items the caller can access, newest first. With no
//     push notifications, this is how a user discovers new feedback.
//   - Worklist: open work that needs the caller's resolution or validation.
//   - General: the shared standalone suggestion channel.
// A thread opens in a right-side slide-over with a link back to its item.
export function FeedbackPage({ onNavigate }: Props) {
  const [tab, setTab] = useState<Tab>("recent");
  const [openThreadId, setOpenThreadId] = useState<string | null>(null);
  const [composing, setComposing] = useState(false);

  // Switching tabs closes any open slide-over so it never lingers over a
  // different tab's content.
  const selectTab = (next: Tab) => {
    setTab(next);
    setOpenThreadId(null);
    setComposing(false);
  };

  const practitioner = usePractitionerWorklist();
  const sme = useSMEWorklist();
  const worklistCount = (practitioner.data?.total ?? 0) + (sme.data?.total ?? 0);

  const tabs: { key: Tab; label: string; icon: typeof Clock; badge?: number }[] = [
    { key: "recent", label: "Recent", icon: Clock },
    { key: "worklist", label: "Worklist", icon: Inbox, badge: worklistCount },
    { key: "general", label: "General", icon: Megaphone },
  ];

  return (
    <div className="flex h-full flex-col gap-4">
      <div className="flex items-start justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-lg font-semibold">
            <MessageCircle className="h-5 w-5 text-primary" /> Feedback
          </h1>
          <p className="text-sm text-muted-foreground">
            Feedback across everything you can access, your open worklist, and the shared channel.
          </p>
        </div>
        <button
          type="button"
          onClick={() => {
            setOpenThreadId(null);
            setComposing(true);
          }}
          className="flex shrink-0 items-center gap-1.5 rounded-md bg-primary px-3 py-1.5 text-sm font-medium text-primary-foreground hover:bg-primary/90"
        >
          <Plus className="h-4 w-4" /> New feedback
        </button>
      </div>

      <div className="flex items-center gap-1 border-b">
        {tabs.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => selectTab(t.key)}
            className={cn(
              "-mb-px flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium transition-colors",
              tab === t.key
                ? "border-primary text-foreground"
                : "border-transparent text-muted-foreground hover:text-foreground",
            )}
          >
            <t.icon className="h-4 w-4" /> {t.label}
            {t.badge ? (
              <span className="rounded-full bg-primary/15 px-1.5 text-[11px] font-semibold text-primary">
                {t.badge}
              </span>
            ) : null}
          </button>
        ))}
      </div>

      <div className="min-h-0 flex-1 overflow-hidden rounded-lg border bg-card">
        {tab === "recent" && (
          <div className="h-full overflow-auto">
            <ActivityFeed onOpenThread={setOpenThreadId} onNavigate={onNavigate} />
          </div>
        )}
        {tab === "worklist" && <InboxPanel onOpenThread={setOpenThreadId} />}
        {tab === "general" && <FeedbackPanel target={{ type: "standalone" }} canModerate={false} />}
      </div>

      {openThreadId && (
        <ThreadSlideOver
          threadId={openThreadId}
          onClose={() => setOpenThreadId(null)}
          onNavigate={onNavigate}
        />
      )}

      {composing && (
        <SlideOver onClose={() => setComposing(false)}>
          <div className="border-b px-4 py-2 text-xs text-muted-foreground">
            Posting to the General channel, visible to everyone on the platform.
          </div>
          <div className="min-h-0 flex-1 overflow-auto">
            <NewThreadForm
              target={{ type: "standalone" }}
              availableAnchor={null}
              onCancel={() => setComposing(false)}
              onCreated={(threadId) => {
                setComposing(false);
                setTab("general");
                setOpenThreadId(threadId);
              }}
            />
          </div>
        </SlideOver>
      )}
    </div>
  );
}
