import { useState } from "react";
import { APIGatewayView } from "./APIGatewayView";
import { HealthView } from "./HealthView";
import { IndexingPage } from "@/pages/indexing/IndexingPage";
import { OverviewTab } from "./tabs/OverviewTab";
import { EventsTab } from "./tabs/EventsTab";
import { NotificationsTab } from "./tabs/NotificationsTab";

type Tab = "mcp" | "apigateway" | "health" | "indexing" | "events" | "notifications";

const TAB_ITEMS: { key: Tab; label: string }[] = [
  { key: "mcp", label: "MCP" },
  { key: "apigateway", label: "API Gateway" },
  { key: "health", label: "Health" },
  { key: "indexing", label: "Indexing" },
  { key: "events", label: "Events" },
  { key: "notifications", label: "Notifications" },
];

export function AuditLogPage({ initialTab, onNavigate }: { initialTab?: string; onNavigate?: (path: string) => void }) {
  const [tab, setTab] = useState<Tab>(
    (TAB_ITEMS.some((t) => t.key === initialTab) ? initialTab : "mcp") as Tab,
  );

  return (
    <div className="space-y-4">
      {/* Tab bar */}
      <div className="flex gap-1 border-b">

        {TAB_ITEMS.map((t) => (
          <button
            key={t.key}
            onClick={() => setTab(t.key)}
            className={`px-4 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? "border-b-2 border-primary text-primary"
                : "text-muted-foreground hover:text-foreground"
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {tab === "mcp" && <OverviewTab onNavigate={onNavigate} />}
      {tab === "apigateway" && <APIGatewayView />}
      {tab === "health" && <HealthView />}
      {tab === "indexing" && <IndexingPage />}
      {tab === "events" && <EventsTab onNavigate={onNavigate} />}
      {tab === "notifications" && <NotificationsTab />}
    </div>
  );
}
