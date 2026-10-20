import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScheduleTimelineTab } from "./ScheduleTimelineTab";
import { ScriptListing } from "./ScriptListing";
import { ScriptRunsTab } from "./ScriptRunsTab";
import { EndpointsTab } from "./endpoints/EndpointsTab";
import { ENDPOINTS_PATH } from "./endpoints/endpointLabels";

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
// traffic, at /admin/automations/endpoints.

export function AdminScriptsPage({
  onNavigate,
  tab: initial = "scripts",
}: {
  onNavigate: (path: string) => void;
  /** tab is the tab the address names: Endpoints has its own (#2208). */
  tab?: string;
}) {
  const [tab, setTab] = useState(initial);
  // The Endpoints tab is an address of its own, so a row's back link and a
  // bookmark land on it; the other three share the section's address.
  const choose = (next: string) => {
    if (next === "endpoints" || tab === "endpoints") {
      onNavigate(next === "endpoints" ? ENDPOINTS_PATH : "/admin/automations");
    }
    setTab(next);
  };
  return (
    <Tabs value={tab} onValueChange={choose} className="gap-4">
      {/* Four questions, four tabs: what exists, when it fires, what has
          been running, and what answers HTTP. */}
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
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
      </TabsList>

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
