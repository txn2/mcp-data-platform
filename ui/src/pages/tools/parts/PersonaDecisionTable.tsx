import { StatusBadge } from "@/components/cards/StatusBadge";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import type { ToolPersonaAccess } from "@/api/admin/types";

// PersonaDecisionTable is the one rendering of "who may call this tool": the
// persona, the decision, the rule that produced it, and where that rule came
// from. Overview and Visibility both answer that question, so they answer it
// the same way instead of each restating the table.
export function PersonaDecisionTable({ personas }: { personas: ToolPersonaAccess[] }) {
  return (
    <div className="overflow-hidden rounded border">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/40 hover:bg-muted/40">
            <TableHead className="h-8 px-3 text-xs">Persona</TableHead>
            <TableHead className="h-8 px-3 text-xs">Decision</TableHead>
            <TableHead className="h-8 px-3 text-xs">Matched pattern</TableHead>
            <TableHead className="h-8 px-3 text-xs">Source</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {personas.map((p) => (
            <TableRow key={p.persona}>
              <TableCell className="px-3 py-1.5 font-medium">{p.persona}</TableCell>
              <TableCell className="px-3 py-1.5">
                <StatusBadge variant={p.allowed ? "success" : "neutral"}>
                  {p.allowed ? "allow" : "deny"}
                </StatusBadge>
              </TableCell>
              <TableCell className="px-3 py-1.5 font-mono text-xs">
                {p.matched_pattern || <span className="text-muted-foreground">—</span>}
              </TableCell>
              <TableCell className="px-3 py-1.5 text-xs text-muted-foreground">
                {p.source}
                {!p.connection_allowed && (
                  <span
                    className="ml-1 text-amber-700 dark:text-amber-400"
                    title="Tool rule allows but the persona's connection rules deny this tool's connection. End-to-end the call would be denied."
                  >
                    · connection denied
                  </span>
                )}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
