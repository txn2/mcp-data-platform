import { useState } from "react";
import { Inbox, ClipboardCheck, AtSign, type LucideIcon } from "lucide-react";
import {
  useInfiniteMentionsWorklist,
  useInfinitePractitionerWorklist,
  useInfiniteSMEWorklist,
} from "@/api/portal/hooks";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import type { ThreadWithMeta } from "@/api/portal/types";
import { ThreadStatusBadge } from "./ThreadBadges";
import { KIND_LABEL, formatRelative } from "./meta";

type Tab = "practitioner" | "sme" | "mentions";

const TAB_ITEMS: { key: Tab; label: string; icon: LucideIcon }[] = [
  { key: "practitioner", label: "Needs resolution", icon: Inbox },
  { key: "sme", label: "Awaiting my validation", icon: ClipboardCheck },
  { key: "mentions", label: "Mentions of me", icon: AtSign },
];

// InboxPanel is the feedback worklist / inbox (#603): self-scoped tabs so
// nothing is dropped — open work that needs the practitioner's resolution,
// validation requests awaiting the SME's response, and the threads where a
// comment addressed the user by name (#627).
export function InboxPanel({ onOpenThread }: { onOpenThread?: (id: string) => void }) {
  const [tab, setTab] = useState<Tab>("practitioner");
  const practitioner = useInfinitePractitionerWorklist();
  const sme = useInfiniteSMEWorklist();
  const mentions = useInfiniteMentionsWorklist();
  const active = { practitioner, sme, mentions }[tab];
  const totals: Record<Tab, number | undefined> = {
    practitioner: practitioner.data?.total,
    sme: sme.data?.total,
    mentions: mentions.data?.total,
  };
  const threads = active.data?.data ?? [];

  return (
    <Tabs
      value={tab}
      onValueChange={(v) => setTab(v as Tab)}
      className="h-full gap-0"
    >
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
      >
        {TAB_ITEMS.map((t) => (
          <TabsTrigger
            key={t.key}
            value={t.key}
            className="flex-1 px-3 py-2 text-xs group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
          >
            <t.icon /> {t.label}
            {totals[t.key] ? (
              <Badge variant="muted" className="px-1.5 text-[10px]">
                {totals[t.key]}
              </Badge>
            ) : null}
          </TabsTrigger>
        ))}
      </TabsList>

      {/* The three worklists share one row shape, so each tab's panel is the
          same list over whichever query the active tab selected. Only the
          active panel mounts, so the body is written once. */}
      {TAB_ITEMS.map((t) => (
        <TabsContent key={t.key} value={t.key} className="min-h-0 overflow-auto">
          {active.isLoading && <p className="p-3 text-xs text-muted-foreground">Loading…</p>}
          {active.isError && (
            <Alert variant="destructive" className="m-3 w-auto">
              <AlertDescription>Failed to load your worklist.</AlertDescription>
            </Alert>
          )}
          {!active.isLoading && !active.isError && threads.length === 0 && (
            <EmptyState className="m-3">Nothing here. You&apos;re all caught up.</EmptyState>
          )}
          <ul className="divide-y">
            {threads.map((thread) => (
              <WorklistRow
                key={thread.id}
                thread={thread}
                onOpen={() => onOpenThread?.(thread.id)}
              />
            ))}
          </ul>
          <div className="p-3">
            <InfiniteFooter
              hasMore={active.hasNextPage}
              isLoadingMore={active.isFetchingNextPage}
              onLoadMore={active.fetchNextPage}
            />
          </div>
        </TabsContent>
      ))}
    </Tabs>
  );
}

function WorklistRow({ thread: t, onOpen }: { thread: ThreadWithMeta; onOpen: () => void }) {
  return (
    <li>
      <button
        type="button"
        onClick={onOpen}
        className="flex w-full items-center gap-2 px-3 py-2 text-left text-sm hover:bg-accent"
      >
        <span className="shrink-0 text-xs text-muted-foreground">{KIND_LABEL[t.kind]}</span>
        <span className="min-w-0 flex-1 truncate">{t.title || "(untitled feedback)"}</span>
        <ThreadStatusBadge status={t.status} />
        {t.last_event_at && (
          <span className="shrink-0 text-[10px] text-muted-foreground">
            {formatRelative(t.last_event_at)}
          </span>
        )}
      </button>
    </li>
  );
}
