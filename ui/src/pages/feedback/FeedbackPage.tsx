import { useState } from "react";
import { Clock, Inbox, Megaphone, Plus, type LucideIcon } from "lucide-react";
import { usePractitionerWorklist, useSMEWorklist } from "@/api/portal/hooks";
import { ActivityFeed } from "@/components/feedback/ActivityFeed";
import { InboxPanel } from "@/components/feedback/InboxPanel";
import { FeedbackPanel } from "@/components/feedback/FeedbackPanel";
import { ThreadSlideOver } from "@/components/feedback/ThreadSlideOver";
import { SlideOver } from "@/components/feedback/SlideOver";
import { NewThreadForm } from "@/components/feedback/NewThreadForm";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

interface Props {
  onNavigate: (path: string) => void;
}

type Tab = "recent" | "worklist" | "general";

const TAB_ITEMS: { key: Tab; label: string; icon: LucideIcon }[] = [
  { key: "recent", label: "Recent", icon: Clock },
  { key: "worklist", label: "Worklist", icon: Inbox },
  { key: "general", label: "General", icon: Megaphone },
];

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

  return (
    <div className="flex h-full flex-col gap-4">
      {/* The section is named by the header bar and by its intro, so this row
          carries the action alone rather than a third copy of the title. */}
      <div className="flex justify-end">
        <Button
          type="button"
          onClick={() => {
            setOpenThreadId(null);
            setComposing(true);
          }}
        >
          <Plus /> New feedback
        </Button>
      </div>

      <Tabs
        value={tab}
        onValueChange={(v) => selectTab(v as Tab)}
        className="min-h-0 flex-1 gap-4"
      >
        <TabsList
          variant="line"
          className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
        >
          {TAB_ITEMS.map((t) => (
            <TabsTrigger
              key={t.key}
              value={t.key}
              className="flex-none px-3 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
            >
              <t.icon /> {t.label}
              {t.key === "worklist" && worklistCount > 0 ? (
                <Badge variant="info" className="px-1.5 text-[11px]">
                  {worklistCount}
                </Badge>
              ) : null}
            </TabsTrigger>
          ))}
        </TabsList>

        <TabsContent
          value="recent"
          className="min-h-0 overflow-auto rounded-lg border bg-card"
        >
          <ActivityFeed onOpenThread={setOpenThreadId} onNavigate={onNavigate} />
        </TabsContent>
        <TabsContent
          value="worklist"
          className="min-h-0 overflow-hidden rounded-lg border bg-card"
        >
          <InboxPanel onOpenThread={setOpenThreadId} />
        </TabsContent>
        <TabsContent
          value="general"
          className="min-h-0 overflow-hidden rounded-lg border bg-card"
        >
          <FeedbackPanel target={{ type: "standalone" }} canModerate={false} />
        </TabsContent>
      </Tabs>

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
