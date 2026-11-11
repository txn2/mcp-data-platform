import { useCallback, useEffect, useState } from "react";
import { Bell, Clock, ListChecks, Megaphone, Plus, type LucideIcon } from "lucide-react";
import {
  usePractitionerWorklist,
  useSMEWorklist,
  useUnreadNotificationCount,
} from "@/api/portal/hooks";
import { ActivityFeed } from "@/components/feedback/ActivityFeed";
import { InboxPanel } from "@/components/feedback/InboxPanel";
import { FeedbackPanel } from "@/components/feedback/FeedbackPanel";
import { NotificationsPanel } from "@/components/feedback/NotificationsPanel";
import { ThreadSlideOver } from "@/components/feedback/ThreadSlideOver";
import { SlideOver } from "@/components/feedback/SlideOver";
import { NewThreadForm } from "@/components/feedback/NewThreadForm";
import { GeneralFeedbackNotice } from "@/components/feedback/GeneralFeedbackNotice";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { AccessRequestsList } from "@/components/access/AccessRequestsList";

interface Props {
  onNavigate: (path: string) => void;
}

type Tab = "recent" | "worklist" | "general" | "notifications";

const TAB_ITEMS: { key: Tab; label: string; icon: LucideIcon }[] = [
  { key: "recent", label: "Recent", icon: Clock },
  { key: "worklist", label: "Worklist", icon: ListChecks },
  { key: "general", label: "General", icon: Megaphone },
  { key: "notifications", label: "Notifications", icon: Bell },
];

// FeedbackPage is the portal's Inbox (#617, renamed by #1798): one place for
// everything addressed to the reader.
//   - Recent: every thread on items the caller can access, newest first. With no
//     push notifications, this is how a user discovers new feedback.
//   - Worklist: open work that needs the caller's resolution or validation.
//   - General: the shared standalone suggestion channel.
//   - Notifications: what the platform itself has sent them, which used to be
//     filed under Settings beside the preferences that decide what gets sent.
// A thread opens in a right-side slide-over with a link back to its item.
//
// The page returns flowing content. It used to pin itself to `h-full` with a
// scroll box per tab panel, which made it the one portal page that scrolled
// inside a card while `main` (AppShell) stayed still -- so three threads filled
// the viewport and the tab strip never scrolled away. `main` is the only scroll
// container now, as it is on every other page.
// threadInHash is the thread the address names (#2603): a General feedback
// email links to /portal/feedback#thread-<id>.
function threadInHash(): string | null {
  const m = /^#thread-([A-Za-z0-9_-]+)$/.exec(window.location.hash);
  return m?.[1] ?? null;
}

// clearThreadHash takes a #thread- fragment out of the address once the thread
// it named is closed, so a reload does not reopen it.
function clearThreadHash() {
  if (window.location.hash.startsWith("#thread-")) {
    window.history.replaceState(null, "", window.location.pathname + window.location.search);
  }
}

export function FeedbackPage({ onNavigate }: Props) {
  const [tab, setTab] = useState<Tab>(() => (threadInHash() ? "general" : "recent"));
  const [openThreadId, setOpenThreadIdState] = useState<string | null>(threadInHash);
  const [composing, setComposing] = useState(false);

  const setOpenThreadId = useCallback((id: string | null) => {
    if (id === null) clearThreadHash();
    setOpenThreadIdState(id);
  }, []);

  // A link to another thread followed while the page is open opens it.
  useEffect(() => {
    const onHash = () => {
      const id = threadInHash();
      if (id) {
        setTab("general");
        setOpenThreadIdState(id);
      }
    };
    window.addEventListener("hashchange", onHash);
    return () => window.removeEventListener("hashchange", onHash);
  }, []);

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
  const unreadCount = useUnreadNotificationCount().data?.unread ?? 0;

  return (
    <div className="space-y-4">
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

      {/* Requests for access to the reader's items (#2090); the owner's email
          links here. Absent when none are waiting. */}
      <AccessRequestsList />

      <Tabs value={tab} onValueChange={(v) => selectTab(v as Tab)} className="gap-4">
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
              {t.key === "notifications" && unreadCount > 0 ? (
                <Badge
                  variant="info"
                  className="px-1.5 text-[11px]"
                  aria-label={`${unreadCount} unread`}
                >
                  {unreadCount}
                </Badge>
              ) : null}
            </TabsTrigger>
          ))}
        </TabsList>

        {/* No `overflow` on any panel: the page is as tall as its content and
            scrolls with `main`. */}
        <TabsContent value="recent" className="rounded-lg border bg-card">
          <ActivityFeed onOpenThread={setOpenThreadId} onNavigate={onNavigate} />
        </TabsContent>
        <TabsContent value="worklist" className="rounded-lg border bg-card">
          <InboxPanel onOpenThread={setOpenThreadId} />
        </TabsContent>
        <TabsContent value="general" className="rounded-lg border bg-card">
          <FeedbackPanel target={{ type: "standalone" }} canModerate={false} flow />
        </TabsContent>
        <TabsContent value="notifications">
          <NotificationsPanel />
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
          <div className="border-b px-4 py-2">
            <GeneralFeedbackNotice />
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
