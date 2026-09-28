import { ShieldCheck } from "lucide-react";
import { usePreHarnessScripts } from "@/api/admin/hooks/scripts";
import type { PreHarnessScript } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { formatWhen } from "./runFormat";

// PreHarnessTab is the administrator's view of the automations saved before
// lint and tests were required (#1943) that still carry lint findings or have
// no tests, with how many of each, so the older set can be brought up over
// time. Each one keeps saving as it did; one with no finding and at least one
// test drops off the list. A row opens the automation, where its owner or an
// administrator edits it.
//
// It is an administrator's page: the route behind it is admin-only, and the
// tab is on the administrator's Automations section alone.
export function PreHarnessTab({
  basePath,
  onNavigate,
}: {
  basePath: string;
  onNavigate: (path: string) => void;
}) {
  const { data, isLoading, error } = usePreHarnessScripts();
  const rows = data?.data ?? [];
  return (
    <SectionCard title="Saved before tests">
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Automations saved before lint and tests were required, that still have lint findings or
          no tests. Each one keeps running and saving as it did. Clearing its findings and giving it
          a test takes it off this list.
        </p>
        {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
        {error && (
          <Alert variant="destructive">
            <AlertDescription>These automations could not be loaded.</AlertDescription>
          </Alert>
        )}
        {data && data.pre_harness > data.examined && (
          <Alert>
            <AlertDescription>
              {data.pre_harness} automations were saved before tests were required; the first{" "}
              {data.examined} by name were checked.
            </AlertDescription>
          </Alert>
        )}
        {data && rows.length === 0 && (
          <EmptyState icon={ShieldCheck}>
            Every automation saved before tests has been brought up to date.
          </EmptyState>
        )}
        {rows.length > 0 && (
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Automation</TableHead>
                <TableHead>Owner</TableHead>
                <TableHead className="text-right">Lint findings</TableHead>
                <TableHead className="text-right">Tests</TableHead>
                <TableHead>Updated</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <PreHarnessRow
                  key={row.id}
                  row={row}
                  onOpen={() => onNavigate(`${basePath}/${row.id}`)}
                />
              ))}
            </TableBody>
          </Table>
        )}
      </div>
    </SectionCard>
  );
}

function PreHarnessRow({ row, onOpen }: { row: PreHarnessScript; onOpen: () => void }) {
  return (
    <TableRow className="cursor-pointer" onClick={onOpen}>
      <TableCell className="max-w-md">
        <div className="font-medium">{row.display_name || row.name}</div>
        <div className="font-mono text-xs text-muted-foreground">{row.name}</div>
      </TableCell>
      <TableCell>{row.owner_email || "nobody"}</TableCell>
      <TableCell className="text-right tabular-nums">{row.lint_findings}</TableCell>
      <TableCell className="text-right tabular-nums">{row.tests}</TableCell>
      <TableCell className="whitespace-nowrap text-muted-foreground">
        {formatWhen(row.updated_at)}
      </TableCell>
    </TableRow>
  );
}
