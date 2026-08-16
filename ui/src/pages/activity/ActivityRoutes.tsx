import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ActivityPage } from "./ActivityPage";
import { MySessionDetailPage } from "./MySessionDetailPage";
import { MySessionsPage } from "./MySessionsPage";
import {
  ACTIVITY_ROUTE,
  MY_SESSIONS_ROUTE,
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
] as const;

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

  if (route !== ACTIVITY_ROUTE && route !== MY_SESSIONS_ROUTE) return null;
  const active = route === MY_SESSIONS_ROUTE ? "sessions" : "overview";

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
    </Tabs>
  );
}
