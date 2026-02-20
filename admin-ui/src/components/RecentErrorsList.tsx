import { useState } from "react";
import type { AuditEvent } from "@/api/types";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { EventDrawer } from "@/components/EventDrawer";

interface RecentErrorsListProps {
  events: AuditEvent[] | undefined;
  onNavigate?: (path: string) => void;
}

export function RecentErrorsList({ events, onNavigate }: RecentErrorsListProps) {
  const [selectedEvent, setSelectedEvent] = useState<AuditEvent | null>(null);

  if (!events || events.length === 0) {
    return <p className="text-sm text-muted-foreground">No recent errors</p>;
  }

  return (
    <>
      <div className="space-y-2">
        {events.map((e) => (
          <div
            key={e.id}
            onClick={() => setSelectedEvent(e)}
            className="flex cursor-pointer items-start gap-2 rounded p-1 text-xs transition-colors hover:bg-muted/50"
          >
            <StatusBadge variant="error">Error</StatusBadge>
            <div className="min-w-0 flex-1">
              <p className="font-medium">{e.tool_name}</p>
              <p className="truncate text-muted-foreground">
                {e.error_message || "Unknown error"}
              </p>
            </div>
            <span className="shrink-0 text-muted-foreground">
              {new Date(e.timestamp).toLocaleTimeString()}
            </span>
          </div>
        ))}
      </div>
      {selectedEvent && (
        <EventDrawer
          event={selectedEvent}
          onClose={() => setSelectedEvent(null)}
          onNavigate={onNavigate}
        />
      )}
    </>
  );
}
