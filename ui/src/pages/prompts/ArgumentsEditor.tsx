import type { Prompt } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Textarea } from "@/components/ui/textarea";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { cn } from "@/lib/utils";

// ArgumentsEditor is the editable arguments table both authoring forms show
// under the content editor. The rows themselves are derived from the content —
// a placeholder typed above adds one, deleting the placeholder removes it — so
// the only editable fields here are what an argument means and whether it is
// required. Shared by the personal create form and the viewer's edit form so
// the two cannot drift.
export function ArgumentsEditor({
  args,
  updateArgField,
}: {
  args: Prompt["arguments"];
  updateArgField: (name: string, patch: Partial<Prompt["arguments"][number]>) => void;
}) {
  if (args.length === 0) {
    return (
      <EmptyState>
        No arguments yet. Add a <code className="font-mono">{"{{placeholder}}"}</code> in the
        content above.
      </EmptyState>
    );
  }
  return (
    <div className="overflow-hidden rounded-lg border">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            <TableHead className="px-3 text-xs text-muted-foreground">Name</TableHead>
            <TableHead className="px-3 text-xs text-muted-foreground">Description</TableHead>
            <TableHead className="px-3 text-right text-xs text-muted-foreground">
              Required
            </TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {args.map((a) => (
            <TableRow key={a.name} className="align-top">
              <TableCell className="px-3 py-2 align-top">
                <code className="rounded bg-muted/60 px-1.5 py-0.5 font-mono text-xs break-all">
                  {`{{${a.name}}}`}
                </code>
              </TableCell>
              <TableCell className="w-full px-3 py-2 align-top whitespace-normal">
                <Textarea
                  value={a.description}
                  onChange={(e) => updateArgField(a.name, { description: e.target.value })}
                  placeholder="What this argument is for"
                  aria-label={`Description for ${a.name}`}
                  rows={2}
                  className="min-h-0 resize-y text-xs md:text-xs"
                />
              </TableCell>
              <TableCell className="px-3 py-2 text-right align-top">
                <label className="inline-flex cursor-pointer items-center gap-2 text-xs select-none">
                  <input
                    type="checkbox"
                    checked={a.required}
                    onChange={(e) => updateArgField(a.name, { required: e.target.checked })}
                    className="size-3.5"
                  />
                  <span
                    className={cn(
                      "font-medium",
                      a.required ? "text-destructive" : "text-muted-foreground",
                    )}
                  >
                    {a.required ? "Required" : "Optional"}
                  </span>
                </label>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}
