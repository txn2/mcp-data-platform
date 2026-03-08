import { useState } from "react";
import type { AuditEvent } from "@/api/admin/types";
import { StatusBadge } from "@/components/cards/StatusBadge";
import { EventDrawer } from "@/components/EventDrawer";
import { formatToolName } from "@/lib/formatToolName";

interface RecentErrorsListProps {
  events: AuditEvent[] | undefined;
  onNavigate?: (path: string) => void;
  titleMap?: Record<string, string>;
}

export function RecentErrorsList({ events, onNavigate, titleMap }: RecentErrorsListProps) {
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
              <p className="font-medium">{formatToolName(e.tool_name, titleMap?.[e.tool_name])}</p>
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
