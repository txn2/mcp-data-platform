import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ActivityPage } from "./ActivityPage";
import { MyCallDetailPage } from "./MyCallDetailPage";
import { MyCallsPage } from "./MyCallsPage";
import { MySessionDetailPage } from "./MySessionDetailPage";
import { MySessionsPage } from "./MySessionsPage";
import {
  ACTIVITY_ROUTE,
  MY_CALLS_ROUTE,
  MY_SESSIONS_ROUTE,
  myCallIdFrom,
  mySessionIdFrom,
} from "./routes";

// ActivityRoutes is the reader's own activity, in two faces of one section:
// the dashboard of aggregates, and the sessions those aggregates are made of.
// It owns its route matching so the shell composes the section rather than each
// of its pages, the same shape the script and admin-session sections take.
//
// The two faces are routes rather than in-page state because a session detail
// has to be addressable — an asset links to the session that made it — and a
// detail hanging off a hash would not survive being handed to someone else.

const TABS = [
  { key: "overview", label: "Overview", path: ACTIVITY_ROUTE },
  { key: "sessions", label: "My Sessions", path: MY_SESSIONS_ROUTE },
  { key: "calls", label: "My Calls", path: MY_CALLS_ROUTE },
] as const;

/** Which tab a route is in. The detail routes are handled before this. */
function activeTab(route: string): (typeof TABS)[number]["key"] {
  if (route === MY_SESSIONS_ROUTE) return "sessions";
  if (route === MY_CALLS_ROUTE) return "calls";
  return "overview";
}

export function ActivityRoutes({
  route,
  onNavigate,
}: {
  route: string;
  onNavigate: (path: string) => void;
}) {
  // The detail is its own page: it carries a back link to the list rather than
  // the tab bar, so the reader is never one stray click from losing the
  // session they opened.
  const sessionId = mySessionIdFrom(route);
  if (sessionId) {
    return (
      <MySessionDetailPage
        sessionId={sessionId}
        onNavigate={onNavigate}
        onBack={() => onNavigate(MY_SESSIONS_ROUTE)}
      />
    );
  }

  const callId = myCallIdFrom(route);
  if (callId) {
    return (
      <MyCallDetailPage
        callId={callId}
        onNavigate={onNavigate}
        onBack={() => onNavigate(MY_CALLS_ROUTE)}
      />
    );
  }

  if (!TABS.some((tab) => tab.path === route)) return null;
  const active = activeTab(route);

  return (
    <Tabs
      value={active}
      onValueChange={(v) => {
        const tab = TABS.find((t) => t.key === v);
        if (tab) onNavigate(tab.path);
      }}
      className="gap-6"
    >
      <TabsList variant="line" className="w-full justify-start border-b">
        {TABS.map((t) => (
          <TabsTrigger key={t.key} value={t.key} className="flex-none px-4 py-2">
            {t.label}
          </TabsTrigger>
        ))}
      </TabsList>

      {/* Each tab is a route, so only the active one is ever on the page.
          It still renders inside TabsContent: that is what the trigger's
          aria-controls points at, and a tab bar controlling nothing is a tab
          bar assistive tech cannot follow. */}
      <TabsContent value="overview">
        <ActivityPage />
      </TabsContent>
      <TabsContent value="sessions">
        <MySessionsPage onNavigate={onNavigate} />
      </TabsContent>
      <TabsContent value="calls">
        <MyCallsPage onNavigate={onNavigate} />
      </TabsContent>
    </Tabs>
  );
}
