import { useState } from "react";

import {
  useInfiniteMentionsWorklist,
  useInfinitePractitionerWorklist,
  useInfiniteSMEWorklist,
} from "@/api/portal/hooks";
import { InfiniteFooter } from "@/components/InfiniteFooter";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { FilterChip } from "@/components/FilterChip";
import type { ThreadWithMeta } from "@/api/portal/types";
import { ThreadStatusBadge } from "./ThreadBadges";
import { KIND_LABEL, formatRelative } from "./meta";

type Tab = "practitioner" | "sme" | "mentions";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "practitioner", label: "Needs resolution" },
  { key: "sme", label: "Awaiting my validation" },
  { key: "mentions", label: "Mentions of me" },
];

// InboxPanel is the feedback worklist (#603): three self-scoped lists so
// nothing is dropped — open work that needs the practitioner's resolution,
// validation requests awaiting the SME's response, and the threads where a
// comment addressed the user by name (#627).
//
// The three are selected by a chip row, not by tabs. This panel already sits
// inside the Inbox's tab strip, and a second strip directly under the first
// read as two competing levels of the same control; chips are how the other
// portal lists select a subset (#1798). The counts move onto the chips, so
// nothing is lost with the tabs.
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
    <div>
      <div className="flex flex-wrap gap-1.5 border-b p-3">
        {TAB_ITEMS.map((t) => (
          <FilterChip
            key={t.key}
            label={t.label}
            count={totals[t.key]}
            active={tab === t.key}
            onClick={() => setTab(t.key)}
          />
        ))}
      </div>

      {/* The three worklists share one row shape, so the body is written once
          over whichever query the active chip selected. */}
      <WorklistBody query={active} threads={threads} onOpenThread={onOpenThread} />
    </div>
  );
}

// WorklistBody renders the selected list's four states. It is its own
// component so the panel above stays a selector: the states belong to the
// query, not to the choice of query.
function WorklistBody({
  query,
  threads,
  onOpenThread,
}: {
  query: {
    isLoading: boolean;
    isError: boolean;
    hasNextPage?: boolean;
    isFetchingNextPage: boolean;
    fetchNextPage: () => void;
  };
  threads: ThreadWithMeta[];
  onOpenThread?: (id: string) => void;
}) {
  if (query.isError) {
    return (
      <Alert variant="destructive" className="m-3 w-auto">
        <AlertDescription>Failed to load your worklist.</AlertDescription>
      </Alert>
    );
  }
  if (query.isLoading) {
    return <p className="p-3 text-xs text-muted-foreground">Loading&hellip;</p>;
  }
  if (threads.length === 0) {
    return <EmptyState className="m-3">Nothing here. You&apos;re all caught up.</EmptyState>;
  }
  return (
    <>
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
          hasMore={Boolean(query.hasNextPage)}
          isLoadingMore={query.isFetchingNextPage}
          onLoadMore={query.fetchNextPage}
        />
      </div>
    </>
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
