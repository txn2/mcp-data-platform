import { useState } from "react";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScheduleTimelineTab } from "./ScheduleTimelineTab";
import { ScriptListing } from "./ScriptListing";
import { ScriptRunsList } from "./ScriptRunsList";

// MyScriptsPage is Automations for everyone signed in (#1290, #1994): every
// script, what each is scheduled to do, and how its runs have gone, including
// the ones somebody else built and runs for the reader. Acting on a script --
// running, scheduling, changing it -- and what its runs were given and
// printed stay with its owner and administrators.
//
// Three tabs, because there are three questions: what is there, when do they
// fire in relation to each other (#1891), and how have they been running
// (#1405). The last two used to take opening every script in turn to answer.
//
// Every tab is the same component the administrator's section uses (#1407),
// told who is reading.

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
