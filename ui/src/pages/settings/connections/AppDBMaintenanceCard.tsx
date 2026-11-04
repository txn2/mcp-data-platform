import { useState } from "react";
import { AlertCircle } from "lucide-react";
import { useAppDBMaintenance } from "@/api/admin/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

// AppDBMaintenanceCard runs maintenance on an application database (#2410):
// ANALYZE, VACUUM, VACUUM FULL and REINDEX CONCURRENTLY, outside a
// transaction as the connection's own login under a short lock timeout.
// Creating or dropping an index is a migration, added with manage_schema.

const COMMANDS: { command: string; label: string; needsTable: boolean; destructive?: boolean }[] = [
  { command: "analyze", label: "Analyze", needsTable: false },
  { command: "vacuum", label: "Vacuum", needsTable: false },
  { command: "vacuum_full", label: "Vacuum full", needsTable: true, destructive: true },
  { command: "reindex", label: "Reindex", needsTable: true },
];

export function AppDBMaintenanceCard({ connectionName }: { connectionName: string }) {
  const [table, setTable] = useState("");
  const run = useAppDBMaintenance(connectionName);
  return (
    <SectionCard title="Maintenance" collapsible defaultOpen={false}>
      <div className="space-y-3">
        <p className="text-xs text-muted-foreground">
          Analyze and vacuum act on the whole database without a table. Vacuum full rewrites the table under an ACCESS
          EXCLUSIVE lock, which blocks every read and write of it until it ends. Reindex rebuilds the table's indexes
          concurrently.
        </p>
        <div className="flex flex-wrap items-center gap-2">
          <Input
            aria-label="Table"
            placeholder="schema.table"
            className="h-8 w-56 font-mono"
            value={table}
            onChange={(e) => setTable(e.target.value)}
          />
          {COMMANDS.map((c) => (
            <Button
              key={c.command}
              size="sm"
              variant={c.destructive ? "destructive" : "outline"}
              disabled={run.isPending || (c.needsTable && !table)}
              onClick={() => run.mutate({ command: c.command, table: table || undefined })}
            >
              {c.label}
            </Button>
          ))}
        </div>
        {run.error && (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertDescription>{run.error.message}</AlertDescription>
          </Alert>
        )}
        {run.data && (
          <p className="text-xs" data-testid="appdb-maintenance-outcome">
            <span className="font-mono">{run.data.statement}</span> ran in {run.data.duration_ms} ms.
          </p>
        )}
      </div>
    </SectionCard>
  );
}
