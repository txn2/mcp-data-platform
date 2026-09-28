import type { ScriptContract } from "@/api/portal/hooks/scripts";
import { PrincipalLabel } from "@/components/PrincipalLabel";
import { SectionCard } from "@/components/patterns/SectionCard";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

// ScriptUsedBy is a library's page section (#1941): how another script loads
// it, and which scripts do today.
//
// It stands where an automation's schedule, runs and state stand, because a
// library has none of those: it is never run or scheduled itself. What a
// reader of a library needs instead is the line to load it with and the
// scripts a change to it reaches, each at the library version it loads, since
// a script pins the version it loads and is not moved by a newer save.
//
// A row opens that script, the way every portal list opens a record.

interface Props {
  contract: ScriptContract;
  /** basePath is the section a row opens under: /automations or /admin/automations. */
  basePath: string;
  onNavigate: (path: string) => void;
}

export function ScriptUsedBy({ contract, basePath, onNavigate }: Props) {
  const users = contract.used_by ?? [];
  return (
    <SectionCard title="Used by">
      <div className="space-y-3">
        <div className="space-y-1">
          <p className="text-xs text-muted-foreground">
            Another script loads version {contract.version} of this library with:
          </p>
          <code
            className="block overflow-x-auto rounded-md border bg-muted/40 px-2 py-1.5 font-mono text-xs"
            data-testid="library-load-line"
          >
            {loadLine(contract.name, contract.version)}
          </code>
        </div>
        {users.length === 0 ? (
          <p className="text-sm text-muted-foreground">No automation loads this library.</p>
        ) : (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Automation</TableHead>
                <TableHead>Author</TableHead>
                <TableHead>Loads</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((use) => (
                <TableRow
                  key={use.script_id}
                  className="cursor-pointer"
                  onClick={() => onNavigate(`${basePath}/${use.script_id}`)}
                  data-testid={`used-by-${use.script_id}`}
                >
                  <TableCell className="whitespace-normal">
                    <div className="font-medium">{use.display_name || use.name}</div>
                    <div className="font-mono text-xs text-muted-foreground">{use.name}</div>
                  </TableCell>
                  <TableCell className="text-xs">
                    {use.owner_email ? (
                      <PrincipalLabel userId={use.owner_email} email={use.owner_email} />
                    ) : (
                      <span className="text-muted-foreground">nobody</span>
                    )}
                  </TableCell>
                  <TableCell className="text-xs whitespace-nowrap">version {use.version}</TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        )}
      </div>
    </SectionCard>
  );
}

// loadLine is the statement a script loads this library's current version
// with; the names it imports are the loading script's to choose.
export function loadLine(name: string, version: number): string {
  return `load("lib:${name}@${version}", ...)`;
}
