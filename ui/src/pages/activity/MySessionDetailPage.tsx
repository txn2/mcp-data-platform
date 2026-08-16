import { useState } from "react";
import { History } from "lucide-react";
import { useMySession } from "@/api/portal/hooks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import {
  SessionDetailHeader,
  SessionDetailView,
  TIMELINE_PER_PAGE,
} from "@/pages/sessions/SessionDetailView";
import { SessionTimeline } from "@/pages/sessions/SessionTimeline";

// MySessionDetailPage is one of the reader's own sessions opened: what it did,
// in order, with the reason stated for each call, and the assets it saved
// linked back to where the reader reads them.
//
// A session that is not the reader's own is not found rather than refused, so
// the empty state below covers both a session that aged out of audit history
// and an id that was never the reader's — deliberately, since distinguishing
// them would be an answer about someone else's activity.

/** Stable empty map, so the timeline does not re-render on every pass. */
const NO_TOOL_TITLES: Record<string, string> = {};

export function MySessionDetailPage({
  sessionId,
  onNavigate,
  onBack,
}: {
  sessionId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}) {
  const [page, setPage] = useState(1);
  const { data, isLoading, error } = useMySession(
    sessionId,
    page,
    TIMELINE_PER_PAGE,
  );

  const header = (
    <SessionDetailHeader
      session={data}
      sessionId={sessionId}
      backLabel="My Sessions"
      onBack={onBack}
      icon={History}
      showUser={false}
    />
  );

  if (error) {
    return (
      <div className="space-y-4">
        {header}
        <EmptyState icon={History}>
          No calls of yours are recorded for this session. It may have aged out
          of audit history.
        </EmptyState>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {header}
      {data ? (
        <SessionDetailView
          session={data}
          page={page}
          onPage={setPage}
          onNavigate={onNavigate}
          assetPath={(assetId) => `/assets/${assetId}`}
          // No title map: the tool catalogue is an admin-only read, and a user
          // surface must not fire an admin call it will be refused. Without a
          // title, a tool name formats from the name itself.
          titleMap={NO_TOOL_TITLES}
        />
      ) : (
        <SectionCard title="Timeline">
          <SessionTimeline isLoading={isLoading} titleMap={NO_TOOL_TITLES} />
        </SectionCard>
      )}
    </div>
  );
}
