import { useId, useState } from "react";
import { ApiError } from "@/api/admin/client";
import { useAdminTags, useMergeTags, useRenameTag, type RetagResult } from "@/api/admin/hooks/tags";
import type { TagRow } from "@/api/portal/hooks/bulk";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";

// AdminTagsPage is Admin > Tags (#2508): every tag on every asset with how
// many carry it, compared without case, with rename and merge. Both rewrite
// the tag on every asset that carries it and are recorded in the audit log.

type Editing = { mode: "rename" | "merge"; row: TagRow } | null;

/** The spellings of a tag as the table lists them: "finance (112), Finance (9)". */
export function spellingsLine(row: TagRow): string {
  return row.spellings.map((s) => `${s.tag} (${s.count})`).join(", ");
}

/** The existing tag a name would land on, without case, other than the one being changed. */
export function existingTarget(rows: TagRow[], name: string, from: TagRow): TagRow | undefined {
  const want = name.trim().toLowerCase();
  if (!want || want === from.tag.toLowerCase()) return undefined;
  return rows.find((r) => r.tag.toLowerCase() === want);
}

export function AdminTagsPage() {
  const [q, setQ] = useState("");
  const [editing, setEditing] = useState<Editing>(null);
  const [done, setDone] = useState<RetagResult | null>(null);
  const { data, isLoading, isError } = useAdminTags(q);
  const rows = data?.tags ?? [];

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-end justify-between gap-4">
        <p className="max-w-none text-sm text-muted-foreground">
          Every tag on every asset, with how many carry it. Tags are compared without regard to case:
          &ldquo;Finance&rdquo; and &ldquo;finance&rdquo; are one tag here. Rename and merge are recorded in the
          audit log.
        </p>
        <label className="flex flex-col gap-1 text-xs text-muted-foreground">
          Find a tag
          <Input type="search" value={q} onChange={(e) => setQ(e.target.value)} className="h-11 w-full sm:h-9 sm:w-60" />
        </label>
      </div>
      {done && (
        <Alert>
          <AlertDescription>
            {done.moved} {done.moved === 1 ? "asset now carries" : "assets now carry"} &ldquo;{done.tag}&rdquo;.
          </AlertDescription>
        </Alert>
      )}
      {isError && (
        <Alert variant="destructive">
          <AlertDescription>The tags could not be read.</AlertDescription>
        </Alert>
      )}
      <TagTable
        rows={rows}
        loading={isLoading}
        searching={q.trim() !== ""}
        onEdit={(mode, row) => {
          setDone(null);
          setEditing({ mode, row });
        }}
      />
      <RetagDialog
        editing={editing}
        rows={rows}
        onClose={() => setEditing(null)}
        onDone={(res) => {
          setEditing(null);
          setDone(res);
        }}
      />
    </div>
  );
}

function TagTable({
  rows,
  loading,
  searching,
  onEdit,
}: {
  rows: TagRow[];
  loading: boolean;
  searching: boolean;
  onEdit: (mode: "rename" | "merge", row: TagRow) => void;
}) {
  if (loading) return <p className="py-12 text-center text-sm text-muted-foreground">Loading...</p>;
  if (rows.length === 0) {
    return (
      <div className="rounded-lg border border-dashed p-4 text-center text-sm text-muted-foreground">
        {searching ? "No tag begins with that." : "No asset carries a tag yet."}
      </div>
    );
  }
  return (
    <Card className="gap-0 overflow-x-auto py-0">
      <Table>
        <TableHeader>
          <TableRow className="bg-muted/50">
            <TableHead>Tag</TableHead>
            <TableHead>Spellings in use</TableHead>
            <TableHead className="text-right">Assets</TableHead>
            <TableHead />
          </TableRow>
        </TableHeader>
        <TableBody>
          {rows.map((row) => (
            <TableRow key={row.tag.toLowerCase()}>
              <TableCell className="font-medium">{row.tag}</TableCell>
              <TableCell className="whitespace-normal text-muted-foreground">{spellingsLine(row)}</TableCell>
              <TableCell className="text-right">{row.count}</TableCell>
              <TableCell className="text-right whitespace-nowrap">
                <Button variant="outline" size="sm" className="mr-2" onClick={() => onEdit("rename", row)}>
                  Rename
                </Button>
                <Button variant="outline" size="sm" onClick={() => onEdit("merge", row)}>
                  Merge into…
                </Button>
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </Card>
  );
}

function RetagDialog({
  editing,
  rows,
  onClose,
  onDone,
}: {
  editing: Editing;
  rows: TagRow[];
  onClose: () => void;
  onDone: (res: RetagResult) => void;
}) {
  return (
    <Dialog open={editing !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        {editing && (
          <RetagForm key={`${editing.mode}:${editing.row.tag}`} editing={editing} rows={rows} onClose={onClose} onDone={onDone} />
        )}
      </DialogContent>
    </Dialog>
  );
}

function RetagForm({
  editing,
  rows,
  onClose,
  onDone,
}: {
  editing: NonNullable<Editing>;
  rows: TagRow[];
  onClose: () => void;
  onDone: (res: RetagResult) => void;
}) {
  const id = useId();
  const { mode, row } = editing;
  const [name, setName] = useState(mode === "rename" ? row.tag : "");
  const rename = useRenameTag();
  const merge = useMergeTags();
  const pending = rename.isPending || merge.isPending;
  const error = rename.error ?? merge.error;
  const target = existingTarget(rows, name, row);
  const trimmed = name.trim();
  const unchanged = trimmed === "" || trimmed === row.tag;
  const merging = mode === "merge" || !!target;

  function submit() {
    const opts = { onSuccess: onDone };
    if (merging) {
      merge.mutate({ from: row.spellings.map((s) => s.tag), into: target?.tag ?? trimmed }, opts);
      return;
    }
    rename.mutate({ from: row.tag, to: trimmed }, opts);
  }

  return (
    <>
      <DialogHeader>
        <DialogTitle>
          {mode === "rename" ? "Rename" : "Merge"} &ldquo;{row.tag}&rdquo;
        </DialogTitle>
        <DialogDescription>{impact(mode, row, trimmed, target)}</DialogDescription>
      </DialogHeader>
      <div className="space-y-1.5">
        <Label htmlFor={id}>{mode === "rename" ? "New name" : "Into"}</Label>
        <Input id={id} value={name} autoFocus onChange={(e) => setName(e.target.value)} />
      </div>
      <RetagError error={error} />
      <div className="flex justify-end gap-2 pt-2">
        <Button variant="secondary" onClick={onClose}>
          Cancel
        </Button>
        <Button disabled={unchanged || pending} onClick={submit}>
          {submitLabel(pending, merging)}
        </Button>
      </div>
    </>
  );
}

function submitLabel(pending: boolean, merging: boolean): string {
  if (pending) return "Working...";
  return merging ? "Merge" : "Rename";
}

function RetagError({ error }: { error: unknown }) {
  if (!error) return null;
  return (
    <p role="alert" className="text-sm text-destructive">
      {error instanceof ApiError ? error.message : "The tag could not be changed."}
    </p>
  );
}

/** What a rename or merge will do, said before it is confirmed. */
export function impact(mode: "rename" | "merge", row: TagRow, name: string, target?: TagRow): string {
  const moves = `${row.count} ${row.count === 1 ? "asset" : "assets"}`;
  if (target) {
    return `Merge ${row.tag} into ${target.tag}: ${moves} move. ${target.tag} goes to at most ${target.count + row.count} assets (an asset carrying both keeps one). ${row.tag} is removed.`;
  }
  if (mode === "merge") {
    return name ? `${moves} move to ${name}, which is not a tag yet.` : "Name the tag to merge into.";
  }
  return `${moves} change. If the new name is already a tag, this becomes a merge and says so here.`;
}
