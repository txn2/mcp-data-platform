import { useState, useEffect, useCallback, useRef } from "react";
import { Plus, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import {
  Table,
  TableBody,
  TableCell,
  TableRow,
} from "@/components/ui/table";

// The two map editors the connection forms use: a plain one for
// catalog mappings and a masked one for static headers. Split out of
// fields.tsx so the labelled-control primitives and the map editors are
// each reviewable on their own.

export function KeyValueEditor({
  entries,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  entries: Record<string, string>;
  onChange: (entries: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  const [newKey, setNewKey] = useState("");
  const [newValue, setNewValue] = useState("");
  const items = Object.entries(entries);

  const add = () => {
    const k = newKey.trim();
    const v = newValue.trim();
    if (k && v) {
      onChange({ ...entries, [k]: v });
      setNewKey("");
      setNewValue("");
    }
  };

  return (
    <div>
      {items.length > 0 && (
        <div className="mb-2 overflow-hidden rounded-md border">
          <Table className="text-xs">
            <TableBody>
              {items.map(([k, v]) => (
                <TableRow key={k}>
                  <TableCell className="font-mono">{k}</TableCell>
                  <TableCell className="w-6 px-2 text-muted-foreground">&rarr;</TableCell>
                  <TableCell className="font-mono">{v}</TableCell>
                  <TableCell className="w-10 text-right">
                    <Button
                      type="button"
                      variant="ghost"
                      size="icon-xs"
                      onClick={() => {
                        const next = { ...entries };
                        delete next[k];
                        onChange(next);
                      }}
                      aria-label={`Remove ${k}`}
                      className="text-muted-foreground hover:text-destructive"
                    >
                      <X />
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>
        </div>
      )}
      <div className="flex gap-2">
        <Input
          type="text"
          value={newKey}
          onChange={(e) => setNewKey(e.target.value)}
          placeholder={keyPlaceholder ?? "key"}
          aria-label={keyPlaceholder ?? "key"}
          className="h-8 w-36 font-mono text-xs"
        />
        <Input
          type="text"
          value={newValue}
          onChange={(e) => setNewValue(e.target.value)}
          onKeyDown={(e) => { if (e.key === "Enter") { e.preventDefault(); add(); } }}
          placeholder={valuePlaceholder ?? "value"}
          aria-label={valuePlaceholder ?? "value"}
          className="h-8 flex-1 font-mono text-xs"
        />
        <Button
          type="button"
          variant="outline"
          size="icon-sm"
          onClick={add}
          disabled={!newKey.trim() || !newValue.trim()}
          aria-label="Add entry"
        >
          <Plus />
        </Button>
      </div>
    </div>
  );
}

// SensitiveKeyValueEditor renders one inline-editable row per entry
// plus an "Add header" button that appends a fresh empty row. Every
// keystroke commits to the parent's entries map (a row contributes
// only when BOTH name and value are non-empty), so there is no
// "pending uncommitted" state to lose at save time. Stable row IDs
// keep React's reconciliation correct as the user renames keys or
// removes rows out of order. Without IDs, deleting row 1 of 3 would
// look like row 1 had its values changed and row 3 disappeared.
//
// Existing rows come back from the server with value === "[REDACTED]"
// (the redaction mask). The password input renders that as dots; the
// operator selects-all and types to replace the value, and the
// backend's redaction-merge layer reinstates the stored value if the
// row is saved without a change.
export function SensitiveKeyValueEditor({
  entries,
  onChange,
  keyPlaceholder,
  valuePlaceholder,
}: {
  entries: Record<string, string>;
  onChange: (entries: Record<string, string>) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  type Row = { id: number; name: string; value: string };
  const idSeq = useRef(0);
  const nextID = useCallback(() => ++idSeq.current, []);

  // Local rows are the source of truth for editing. Initial value is
  // derived from the entries prop on mount; later sync happens only
  // when the prop's KEY SET changes (a real refresh from the server),
  // not on every value change (which would clobber the user's
  // mid-edit local state after every save round-trip turning real
  // values into "[REDACTED]" masks).
  const [rows, setRows] = useState<Row[]>(() =>
    Object.entries(entries).map(([k, v]) => ({ id: nextID(), name: k, value: v })),
  );
  const lastKeySet = useRef(
    Object.keys(entries).slice().sort().join(""),
  );
  useEffect(() => {
    const k = Object.keys(entries).slice().sort().join("");
    if (k !== lastKeySet.current) {
      lastKeySet.current = k;
      setRows(
        Object.entries(entries).map(([key, val]) => ({
          id: nextID(),
          name: key,
          value: val,
        })),
      );
    }
  }, [entries, nextID]);

  const commit = useCallback(
    (updated: Row[]) => {
      setRows(updated);
      const out: Record<string, string> = {};
      for (const r of updated) {
        const n = r.name.trim();
        if (n && r.value.length > 0) {
          out[n] = r.value;
        }
      }
      // Update lastKeySet to match what we're about to emit so the
      // useEffect above doesn't fire a redundant re-sync after our
      // own commit propagates back through the entries prop.
      lastKeySet.current = Object.keys(out).slice().sort().join("");
      onChange(out);
    },
    [onChange],
  );

  const updateRow = useCallback(
    (id: number, patch: Partial<Row>) => {
      commit(rows.map((r) => (r.id === id ? { ...r, ...patch } : r)));
    },
    [rows, commit],
  );

  const deleteRow = useCallback(
    (id: number) => {
      commit(rows.filter((r) => r.id !== id));
    },
    [rows, commit],
  );

  const addRow = useCallback(() => {
    setRows((prev) => [...prev, { id: nextID(), name: "", value: "" }]);
  }, [nextID]);

  return (
    <div className="space-y-2">
      {rows.map((row) => (
        <div key={row.id} className="flex gap-2">
          <Input
            type="text"
            value={row.name}
            onChange={(e) => updateRow(row.id, { name: e.target.value })}
            placeholder={keyPlaceholder ?? "header"}
            aria-label={keyPlaceholder ?? "header"}
            className="h-8 w-56 font-mono text-xs"
          />
          <Input
            type="password"
            value={row.value}
            onChange={(e) => updateRow(row.id, { value: e.target.value })}
            placeholder={valuePlaceholder ?? "value"}
            aria-label={`${row.name || "header"} value`}
            className="h-8 flex-1 font-mono text-xs"
          />
          <Button
            type="button"
            variant="outline"
            size="icon-sm"
            onClick={() => deleteRow(row.id)}
            aria-label={`Remove ${row.name || "header"}`}
            className="text-muted-foreground hover:text-destructive"
          >
            <X />
          </Button>
        </div>
      ))}
      <Button type="button" variant="outline" size="sm" onClick={addRow}>
        <Plus />
        Add header
      </Button>
    </div>
  );
}
