import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { ScriptListing } from "./ScriptListing";
import { ScriptRunsTab } from "./ScriptRunsTab";

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

export function AdminScriptsPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  return (
    <Tabs defaultValue="scripts" className="gap-4">
      {/* Two questions, two tabs: what exists, and what has been running. */}
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

      <TabsContent value="runs">
        <ScriptRunsTab onNavigate={onNavigate} />
      </TabsContent>

      <TabsContent value="scripts">
        <ScriptListing audience="admin" basePath="/admin/scripts" onNavigate={onNavigate} />
      </TabsContent>
    </Tabs>
  );
}
