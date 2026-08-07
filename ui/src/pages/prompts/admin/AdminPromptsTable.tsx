import React, { useState } from "react";
import { ChevronDown, ChevronRight, Copy, Pencil, Trash2 } from "lucide-react";
import type { Prompt } from "@/api/admin/types";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { markdownToPlainText } from "@/lib/markdownText";
import { cn } from "@/lib/utils";
import { SortableHead } from "../primitives";
import { PromptStatusBadge } from "../PromptStatusBadge";
import { AdminScopeBadge } from "./AdminScopeBadge";
import { columns, type SortDir, type SortKey } from "./adminPromptSort";

// AdminPromptsTable is the admin inventory of every prompt on the platform:
// sortable columns, an expandable row carrying the identifiers and content an
// admin needs to diagnose a prompt, and the per-row edit and delete controls.
// Row-local disclosure state (which row is open, which is confirming a delete)
// lives here; the page owns the data and the delete mutation.
export function AdminPromptsTable({
  prompts,
  sortBy,
  sortDir,
  onSort,
  onEdit,
  onDelete,
}: {
  prompts: Prompt[];
  sortBy: SortKey;
  sortDir: SortDir;
  onSort: (key: SortKey) => void;
  onEdit: (p: Prompt) => void;
  onDelete: (id: string) => void;
}) {
  const [expandedId, setExpandedId] = useState<string | null>(null);
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);

  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <Table className="table-fixed">
        <colgroup>
          <col className="w-10" />
          <col className="w-[22%]" />
          <col className="w-[110px]" />
          <col />
          <col className="w-[160px]" />
          <col className="w-[90px]" />
          <col className="w-[180px]" />
        </colgroup>
        <TableHeader>
          <TableRow className="bg-muted/50 hover:bg-muted/50">
            <TableHead className="w-8 px-2" />
            {columns.map((col) => (
              <SortableHead
                key={col.key}
                label={col.label}
                sortKey={col.key}
                sortBy={sortBy}
                sortDir={sortDir}
                onSort={onSort}
                className={cn("px-4", col.width)}
              />
            ))}
            <TableHead className="px-4 text-right text-muted-foreground">Actions</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {prompts.map((p) => (
            <React.Fragment key={p.id}>
              <PromptRow
                prompt={p}
                expanded={expandedId === p.id}
                confirming={deleteConfirm === p.id}
                onToggle={() => setExpandedId((prev) => (prev === p.id ? null : p.id))}
                onEdit={() => onEdit(p)}
                onDeleteRequest={() => setDeleteConfirm(p.id)}
                onDeleteCancel={() => setDeleteConfirm(null)}
                onDeleteConfirm={() => onDelete(p.id)}
              />
              {expandedId === p.id && <PromptDetailRow prompt={p} />}
            </React.Fragment>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function PromptRow({
  prompt: p,
  expanded,
  confirming,
  onToggle,
  onEdit,
  onDeleteRequest,
  onDeleteCancel,
  onDeleteConfirm,
}: {
  prompt: Prompt;
  expanded: boolean;
  confirming: boolean;
  onToggle: () => void;
  onEdit: () => void;
  onDeleteRequest: () => void;
  onDeleteCancel: () => void;
  onDeleteConfirm: () => void;
}) {
  return (
    <TableRow className="cursor-pointer" onClick={onToggle}>
      <TableCell className="px-2 py-2 text-muted-foreground">
        {expanded ? <ChevronDown className="size-4" /> : <ChevronRight className="size-4" />}
      </TableCell>
      <TableCell className="px-4 py-2">
        <div className="flex min-w-0 items-center gap-2">
          <span className="truncate font-medium">{p.display_name || p.name}</span>
          {p.scope !== "system" && <PromptStatusBadge status={p.status} />}
        </div>
      </TableCell>
      <TableCell className="px-4 py-2"><AdminScopeBadge scope={p.scope} /></TableCell>
      <TableCell className="truncate px-4 py-2 text-muted-foreground">
        {markdownToPlainText(p.description)}
      </TableCell>
      <TableCell className="truncate px-4 py-2 text-xs text-muted-foreground">
        {p.owner_email || "—"}
      </TableCell>
      <TableCell className="px-4 py-2">
        <Badge variant={p.enabled ? "success" : "muted"}>{p.enabled ? "Active" : "Disabled"}</Badge>
      </TableCell>
      <TableCell className="px-4 py-2 text-right">
        <RowActions
          prompt={p}
          confirming={confirming}
          onEdit={onEdit}
          onDeleteRequest={onDeleteRequest}
          onDeleteCancel={onDeleteCancel}
          onDeleteConfirm={onDeleteConfirm}
        />
      </TableCell>
    </TableRow>
  );
}

function RowActions({
  prompt: p,
  confirming,
  onEdit,
  onDeleteRequest,
  onDeleteCancel,
  onDeleteConfirm,
}: {
  prompt: Prompt;
  confirming: boolean;
  onEdit: () => void;
  onDeleteRequest: () => void;
  onDeleteCancel: () => void;
  onDeleteConfirm: () => void;
}) {
  // System prompts ship with the platform; the API refuses to change them.
  if (p.scope === "system") {
    return <span className="text-xs text-muted-foreground">Read-only</span>;
  }
  // The row itself expands on click, so the controls stop the event.
  return (
    <div className="inline-flex justify-end gap-2" onClick={(e) => e.stopPropagation()}>
      {confirming ? (
        <>
          <Button variant="destructive" size="xs" onClick={onDeleteConfirm}>
            <Trash2 /> Delete
          </Button>
          <Button variant="outline" size="xs" onClick={onDeleteCancel}>
            Cancel
          </Button>
        </>
      ) : (
        <>
          <Button variant="outline" size="xs" onClick={onEdit}>
            <Pencil /> Edit
          </Button>
          <Button
            variant="outline"
            size="xs"
            onClick={onDeleteRequest}
            className="border-destructive/30 text-destructive hover:bg-destructive/10 hover:text-destructive"
          >
            <Trash2 /> Delete
          </Button>
        </>
      )}
    </div>
  );
}

// PromptDetailRow is the expanded row: the identifiers, placement, and content
// an admin needs to tell two similarly named prompts apart.
function PromptDetailRow({ prompt: p }: { prompt: Prompt }) {
  return (
    <TableRow className="bg-muted/20 hover:bg-muted/20">
      <TableCell colSpan={7} className="px-6 py-3 whitespace-normal">
        <div className="space-y-2 text-xs">
          <div className="grid gap-4 sm:grid-cols-3">
            <div><span className="text-muted-foreground">ID:</span> <span className="font-mono select-all">{p.id}</span></div>
            <div><span className="text-muted-foreground">Name:</span> <span className="font-mono select-all">{p.name}</span></div>
            <div><span className="text-muted-foreground">Category:</span> <span>{p.category || "—"}</span></div>
          </div>
          <PromptPlacement prompt={p} />
          <div className="space-y-1">
            <div className="flex items-center justify-between">
              <span className="text-muted-foreground">Prompt Content</span>
              <Button
                variant="ghost"
                size="icon-xs"
                onClick={() => navigator.clipboard.writeText(p.content)}
                aria-label="Copy content"
              >
                <Copy />
              </Button>
            </div>
            <pre className="max-h-48 overflow-auto rounded border bg-muted/50 p-3 font-mono text-xs whitespace-pre-wrap">
              {p.content}
            </pre>
          </div>
        </div>
      </TableCell>
    </TableRow>
  );
}

// PromptPlacement is the optional half of the expanded row: the facts a prompt
// only sometimes carries. Each line is absent rather than empty when the prompt
// has nothing to say for it.
function PromptPlacement({ prompt: p }: { prompt: Prompt }) {
  return (
    <>
      {p.personas?.length > 0 && (
        <div><span className="text-muted-foreground">Personas:</span> {p.personas.join(", ")}</div>
      )}
      {p.tags && p.tags.length > 0 && (
        <div className="flex flex-wrap items-center gap-1.5">
          <span className="text-muted-foreground">Tags:</span>
          {p.tags.map((t) => (
            <Badge key={t} variant="muted" className="text-[11px]">{t}</Badge>
          ))}
        </div>
      )}
      {p.status === "superseded" && p.superseded_by && (
        <div><span className="text-muted-foreground">Superseded by:</span> <span className="font-mono">{p.superseded_by}</span></div>
      )}
      {p.approved_by && (
        <div><span className="text-muted-foreground">Approved by:</span> {p.approved_by}</div>
      )}
      {p.arguments?.length > 0 && (
        <div>
          <span className="text-muted-foreground">Arguments:</span>{" "}
          {p.arguments.map((a) => `{${a.name}}${a.required ? "*" : ""}`).join(", ")}
        </div>
      )}
    </>
  );
}
