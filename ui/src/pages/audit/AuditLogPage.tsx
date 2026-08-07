import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
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
    <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)} className="gap-4">
      {/* The dashboard's primary navigation: underline tabs (the `line`
          variant) so it reads as the page's top level, above any pill bar a
          view renders inside its own panel. */}
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
      >
        {TAB_ITEMS.map((t) => (
          <TabsTrigger
            key={t.key}
            value={t.key}
            // The active underline defaults to sitting below the list; pull it
            // onto the bar's own bottom border so the two are one line.
            className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
          >
            {t.label}
          </TabsTrigger>
        ))}
      </TabsList>

      <TabsContent value="mcp">
        <OverviewTab onNavigate={onNavigate} />
      </TabsContent>
      <TabsContent value="apigateway">
        <APIGatewayView />
      </TabsContent>
      <TabsContent value="health">
        <HealthView />
      </TabsContent>
      <TabsContent value="indexing">
        <IndexingPage />
      </TabsContent>
      <TabsContent value="events">
        <EventsTab onNavigate={onNavigate} />
      </TabsContent>
      <TabsContent value="notifications">
        <NotificationsTab />
      </TabsContent>
    </Tabs>
  );
}
