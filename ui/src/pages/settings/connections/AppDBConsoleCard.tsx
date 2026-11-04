import { useState } from "react";
import { AlertCircle } from "lucide-react";
import { useAppDBConsole, type AppDBConsoleResult } from "@/api/admin/hooks";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Textarea } from "@/components/ui/textarea";

// AppDBConsoleCard is an administrator's console on an application database
// (#2427): statements run in one transaction as the connection's own login,
// READ ONLY unless the toggle is turned off for the run that writes. Every run
// is audited with its SQL text and its parameters' shapes, never their values.

function cell(v: unknown): string {
  if (v === null || v === undefined) return "NULL";
  return typeof v === "object" ? JSON.stringify(v) : String(v);
}

function Results({ res }: { res: AppDBConsoleResult }) {
  return (
    <div className="space-y-3" data-testid="appdb-console-results">
      {res.results.map((r) => (
        <div key={r.index} className="space-y-1">
          <div className="text-xs text-muted-foreground">
            {r.command} {r.rows_affected > 0 ? `(${r.rows_affected} row(s))` : ""}
            {r.truncated ? ", first rows only" : ""}
          </div>
          {r.columns.length > 0 && (
            <div className="max-h-80 overflow-auto">
              <table className="w-full text-xs">
                <thead className="text-left text-muted-foreground">
                  <tr>
                    {r.columns.map((c) => (
                      <th key={c.name} className="pb-1 pr-3 font-normal">
                        {c.name}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {r.rows.map((row, i) => (
                    <tr key={i} className="border-t">
                      {row.map((v, j) => (
                        <td key={j} className="py-1 pr-3 font-mono">
                          {cell(v)}
                        </td>
                      ))}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      ))}
      {res.error && (
        <Alert variant="destructive">
          <AlertCircle />
          <AlertDescription>
            Statement {res.error.index + 1}: {res.error.detail || res.error.summary}. Nothing was committed.
          </AlertDescription>
        </Alert>
      )}
      {res.committed && <p className="text-xs font-medium">Committed.</p>}
    </div>
  );
}

export function AppDBConsoleCard({ connectionName }: { connectionName: string }) {
  const [sql, setSql] = useState("");
  const [readOnly, setReadOnly] = useState(true);
  const run = useAppDBConsole(connectionName);
  return (
    <SectionCard title="Console" collapsible defaultOpen={false}>
      <div className="space-y-3">
        <Textarea
          aria-label="SQL"
          className="min-h-28 font-mono text-xs"
          value={sql}
          onChange={(e) => setSql(e.target.value)}
        />
        <div className="flex items-center gap-3">
          <label className="flex items-center gap-2 text-sm">
            <input type="checkbox" checked={readOnly} onChange={(e) => setReadOnly(e.target.checked)} />
            Read only
          </label>
          <Button
            size="sm"
            variant={readOnly ? "outline" : "destructive"}
            disabled={run.isPending || !sql.trim()}
            onClick={() => run.mutate({ sql, read_only: readOnly })}
          >
            Run
          </Button>
        </div>
        {run.error && (
          <Alert variant="destructive">
            <AlertCircle />
            <AlertDescription>{run.error.message}</AlertDescription>
          </Alert>
        )}
        {run.data && <Results res={run.data} />}
      </div>
    </SectionCard>
  );
}
