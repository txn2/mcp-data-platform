import { useState } from "react";
import { History } from "lucide-react";
import { useAuditEvent, useSession, useToolTitleMap } from "@/api/admin/hooks";
import { EventDrawer } from "@/components/EventDrawer";
import { StatCard } from "@/components/cards/StatCard";
import { EmptyState } from "@/components/patterns/EmptyState";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Pager } from "@/components/patterns/Pager";
import { SectionCard } from "@/components/patterns/SectionCard";
import type { SessionDetail } from "@/api/admin/types";
import { formatDuration } from "@/lib/formatDuration";
import { formatUser } from "@/lib/formatUser";
import { kindDescription, kindLabel } from "./kind";
import { SessionAssets, SessionInsights } from "./SessionOutputs";
import { SessionTimeline } from "./SessionTimeline";

// SessionDetailPage is one session opened: who ran it and over what window,
// what it produced, and the ordered record of its calls. A timeline row opens
// the same event drawer the events table opens, so the session view adds a
// reading order rather than a second rendering of an event.

const TIMELINE_PER_PAGE = 25;

export function SessionDetailPage({
  sessionId,
  onNavigate,
  onBack,
}: {
  sessionId: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}) {
  const [page, setPage] = useState(1);
  const [selectedEventId, setSelectedEventId] = useState<string | null>(null);
  const { data, isLoading, error } = useSession(
    sessionId,
    page,
    TIMELINE_PER_PAGE,
  );
  const { data: selectedEvent } = useAuditEvent(selectedEventId);
  const titleMap = useToolTitleMap();

  const header = (
    <PageHeader
      backLabel="Sessions"
      onBack={onBack}
      icon={History}
      title={data ? kindLabel(data.kind) : "Session"}
      urn={sessionId}
      subtitle={data ? subtitleFor(data) : undefined}
    />
  );

  if (error) {
    return (
      <div className="space-y-4">
        {header}
        <EmptyState icon={History}>
          No calls are recorded for this session. It may have aged out of audit
          history.
        </EmptyState>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {header}
      {data ? (
        <SessionBody
          session={data}
          page={page}
          onPage={setPage}
          onSelectEvent={setSelectedEventId}
          onNavigate={onNavigate}
          titleMap={titleMap}
        />
      ) : (
        <SectionCard title="Timeline">
          <SessionTimeline
            isLoading={isLoading}
            onSelect={setSelectedEventId}
            titleMap={titleMap}
          />
        </SectionCard>
      )}
      {selectedEvent && (
        <EventDrawer
          event={selectedEvent}
          onClose={() => setSelectedEventId(null)}
          onNavigate={onNavigate}
        />
      )}
    </div>
  );
}

/** Everything below the header once the session has been read. */
function SessionBody({
  session,
  page,
  onPage,
  onSelectEvent,
  onNavigate,
  titleMap,
}: {
  session: SessionDetail;
  page: number;
  onPage: (page: number) => void;
  onSelectEvent: (eventId: string) => void;
  onNavigate: (path: string) => void;
  titleMap: Record<string, string>;
}) {
  return (
    <>
      <SessionStats session={session} />

      <div className="grid gap-3 lg:grid-cols-2">
        <SessionAssets assets={session.assets} onNavigate={onNavigate} />
        <SessionInsights insights={session.insights} />
      </div>

      <SectionCard title={`Timeline (${session.timeline_total})`}>
        <SessionTimeline
          entries={session.timeline}
          isLoading={false}
          onSelect={onSelectEvent}
          titleMap={titleMap}
        />
        {session.timeline_total > TIMELINE_PER_PAGE && (
          <div className="pt-3">
            <Pager
              page={page}
              perPage={TIMELINE_PER_PAGE}
              total={session.timeline_total}
              onPage={onPage}
            />
          </div>
        )}
      </SectionCard>
    </>
  );
}

/** The session at a glance: how much it did, and what it left behind. */
function SessionStats({ session }: { session: SessionDetail }) {
  return (
    <div className="grid grid-cols-2 gap-3 sm:grid-cols-5">
      <StatCard label="Calls" value={session.call_count} />
      <StatCard
        label="Failed"
        value={session.failure_count}
        detail={
          session.failure_count > 0 ? "calls returned an error" : undefined
        }
      />
      <StatCard label="Duration" value={sessionDuration(session)} />
      <StatCard label="Assets" value={session.asset_count} />
      <StatCard label="Insights" value={session.insight_count} />
    </div>
  );
}

/** The identity line: who ran the session, as what, and when it started. */
function subtitleFor(session: SessionDetail): string {
  const parts = [
    formatUser(session.user_id, session.user_email),
    session.persona,
    kindDescription(session.kind),
    `started ${new Date(session.started_at).toLocaleString()}`,
  ];
  return parts.filter(Boolean).join(" · ");
}

/** Wall-clock span from the session's first call to its last. */
function sessionDuration(session: SessionDetail): string {
  const ms =
    new Date(session.last_active_at).getTime() -
    new Date(session.started_at).getTime();
  return formatDuration(Math.max(ms, 0));
}
