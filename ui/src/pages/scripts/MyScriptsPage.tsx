import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScriptListing } from "./ScriptListing";
import { ScriptRunsList } from "./ScriptRunsList";

// MyScriptsPage is what the people who own the scripts see (#1290): their
// scripts, what each is scheduled to do, and how its last run went.
//
// Two tabs, because there are two questions (#1405): what do I have, and how
// have they been running. The second one used to take opening every script in
// turn to answer.
//
// Both tabs are the same components the administrator's section uses (#1407),
// told who is reading. Every script here is the reader's own, so there is no
// owner column; the administrator's listing has one, where whose script it is
// is the fact worth showing.

interface Props {
  onNavigate: (path: string) => void;
}

export function MyScriptsPage({ onNavigate }: Props) {
  return (
    <Tabs defaultValue="scripts" className="gap-4">
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
      >
        <TabsTrigger
          value="scripts"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Scripts
        </TabsTrigger>
        <TabsTrigger
          value="runs"
          className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
        >
          Runs
        </TabsTrigger>
      </TabsList>

      <TabsContent value="scripts">
        <ScriptListing audience="owner" basePath="/scripts" onNavigate={onNavigate} />
      </TabsContent>

      <TabsContent value="runs">
        <ScriptRunsList audience="owner" basePath="/scripts" onNavigate={onNavigate} />
      </TabsContent>
    </Tabs>
  );
}
