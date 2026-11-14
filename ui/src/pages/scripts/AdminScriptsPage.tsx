import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScheduleTimelineTab } from "./ScheduleTimelineTab";
import { ScriptListing } from "./ScriptListing";
import { ScriptRunsTab } from "./ScriptRunsTab";
import { EndpointsTab } from "./endpoints/EndpointsTab";
import { ENDPOINTS_PATH } from "./endpoints/endpointLabels";
import { HANDLERS_PATH, HandlersTab } from "./HandlersTab";

// AdminScriptsPage is the administrator's script section: every script on the
// platform, and what has been running (#1307).
//
// The listing is the one the owners read, told that an administrator is
// reading it (#1407): the same columns, the same filters, the same search,
// plus the Owner column — a script is one person's, so its owner is who sees
// it, who may run it, and whose authority a scheduled run presents. A script
// showing no owner belongs to nobody — it was authored by a principal carrying
// no address — and the transfer action on the script's own page is how it gets
// one (#1404).
//
// A row opens the script itself, on the same detail page its owner opens: an
// administrator runs, edits, dry-runs, schedules and reads the history of
// every script exactly as its owner does. This page lists; that page acts.
//
// Endpoints (#2208) lists every automation bound to an HTTP address, with its
// traffic, at /admin/automations/endpoints, and Handlers (#2435) every
// handler of every script, at /admin/automations/handlers.

// TAB_PATHS are the tabs with an address of their own.
const TAB_PATHS: Record<string, string> = { endpoints: ENDPOINTS_PATH, handlers: HANDLERS_PATH };

export function AdminScriptsPage({
  onNavigate,
  tab: initial = "scripts",
}: {
  onNavigate: (path: string) => void;
  /** tab is the tab the address names: Endpoints and Handlers have their own (#2208, #2435). */
  tab?: string;
}) {
  const [tab, setTab] = useState(initial);
  // The Endpoints and Handlers tabs are addresses of their own, so a row's
  // back link and a bookmark land on them; the other three share the
  // section's address.
  const choose = (next: string) => {
    if (TAB_PATHS[next] || TAB_PATHS[tab]) {
      onNavigate(TAB_PATHS[next] ?? "/admin/automations");
    }
    setTab(next);
  };
  return (
    <Tabs value={tab} onValueChange={choose} className="gap-4">
      {/* Five questions, five tabs: what exists, when it fires, what has
          been running, what answers HTTP, and what starts each one. */}
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 overflow-x-auto border-b p-0"
      >
        <TabsTrigger
          value="scripts"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Automations
        </TabsTrigger>
        <TabsTrigger
          value="schedules"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Schedules
        </TabsTrigger>
        <TabsTrigger
          value="runs"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Runs
        </TabsTrigger>
        <TabsTrigger
          value="endpoints"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Endpoints
        </TabsTrigger>
        <TabsTrigger
          value="handlers"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Handlers
        </TabsTrigger>
      </TabsList>

      <TabsContent value="handlers">
        <HandlersTab onNavigate={onNavigate} />
      </TabsContent>

      <TabsContent value="endpoints">
        <EndpointsTab onNavigate={onNavigate} />
      </TabsContent>

      <TabsContent value="schedules">
        <ScheduleTimelineTab
          basePath="/admin/automations"
          onNavigate={onNavigate}
          onShowScripts={() => setTab("scripts")}
        />
      </TabsContent>

      <TabsContent value="runs">
        <ScriptRunsTab onNavigate={onNavigate} />
      </TabsContent>

      <TabsContent value="scripts">
        <ScriptListing audience="admin" basePath="/admin/automations" onNavigate={onNavigate} />
      </TabsContent>
    </Tabs>
  );
}
