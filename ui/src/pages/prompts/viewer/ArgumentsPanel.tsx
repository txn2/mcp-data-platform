import type { Prompt } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import {
  Table,
  TableBody,
  TableCell,
  TableRow,
} from "@/components/ui/table";

// ArgumentsPanel renders the read-only summary of a prompt's declared
// arguments (name, required/optional badge, description).
export function ArgumentsPanel({ args }: { args: Prompt["arguments"] }) {
  if (!args || args.length === 0) return null;
  const required = args.filter((a) => a.required).length;
  const optional = args.length - required;
  return (
    <SectionCard
      title={`Arguments (${args.length})`}
      action={
        <span className="text-[11px] text-muted-foreground">
          {required} required <span className="opacity-50">·</span> {optional} optional
        </span>
      }
    >
      <div className="overflow-hidden rounded-lg border">
        <Table>
          <TableBody>
            {args.map((a) => (
              <TableRow key={a.name} className="align-top hover:bg-transparent">
                <TableCell className="w-[40%] px-3 py-2 align-top">
                  <span className="flex flex-wrap items-center gap-2">
                    <code className="rounded bg-muted/60 px-1.5 py-0.5 font-mono text-xs break-all">
                      {`{{${a.name}}}`}
                    </code>
                    <Badge
                      variant={a.required ? "danger" : "muted"}
                      className="px-1.5 text-[10px] tracking-wide uppercase"
                    >
                      {a.required ? "required" : "optional"}
                    </Badge>
                  </span>
                </TableCell>
                <TableCell className="px-3 py-2 align-top text-xs break-words whitespace-normal text-muted-foreground">
                  {a.description || <span className="italic opacity-60">No description</span>}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </SectionCard>
  );
}
