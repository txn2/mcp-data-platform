import { useState } from "react";
import { History } from "lucide-react";
import { useAuditEvent, useSession, useToolTitleMap } from "@/api/admin/hooks";
import { EventDrawer } from "@/components/EventDrawer";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import {
  SessionDetailHeader,
  SessionDetailView,
  TIMELINE_PER_PAGE,
} from "./SessionDetailView";
import { SessionTimeline } from "./SessionTimeline";

// SessionDetailPage is one session opened by an operator: who ran it and over
// what window, what it produced, and the ordered record of its calls. A
// timeline row opens the same event drawer the events table opens, so the
// session view adds a reading order rather than a second rendering of an event.

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
    <SessionDetailHeader
      session={data}
      sessionId={sessionId}
      backLabel="Sessions"
      onBack={onBack}
      icon={History}
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
        <SessionDetailView
          session={data}
          page={page}
          onPage={setPage}
          onSelectEvent={setSelectedEventId}
          onNavigate={onNavigate}
          assetPath={(assetId) => `/admin/assets/${assetId}`}
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
