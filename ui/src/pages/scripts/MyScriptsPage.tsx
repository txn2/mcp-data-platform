import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScheduleTimelineTab } from "./ScheduleTimelineTab";
import { ScriptListing } from "./ScriptListing";
import { ScriptRunsList } from "./ScriptRunsList";

// MyScriptsPage is what the people who own the scripts see (#1290): their
// scripts, what each is scheduled to do, and how its last run went.
//
// Three tabs, because there are three questions: what do I have, when do they
// fire in relation to each other (#1891), and how have they been running
// (#1405). The last two used to take opening every script in turn to answer.
//
// Every tab is the same component the administrator's section uses (#1407),
// told who is reading. Every script here is the reader's own, so there is no
// owner column; the administrator's listing has one, where whose script it is
// is the fact worth showing.

interface Props {
  onNavigate: (path: string) => void;
}

export function MyScriptsPage({ onNavigate }: Props) {
  // Controlled, so the Schedules tab's empty state can send the reader to the
  // Automations tab, where a schedule is set.
  const [tab, setTab] = useState("scripts");
  return (
    <Tabs value={tab} onValueChange={setTab} className="gap-4">
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
      </TabsList>

      <TabsContent value="scripts">
        <ScriptListing audience="owner" basePath="/automations" onNavigate={onNavigate} />
      </TabsContent>

      <TabsContent value="schedules">
        <ScheduleTimelineTab
          basePath="/automations"
          onNavigate={onNavigate}
          onShowScripts={() => setTab("scripts")}
        />
      </TabsContent>

      <TabsContent value="runs">
        <ScriptRunsList audience="owner" basePath="/automations" onNavigate={onNavigate} />
      </TabsContent>
    </Tabs>
  );
}
