import { useState, useMemo } from "react";
import { Plus, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { matchPattern } from "./resolve";
import { renderPattern } from "./renderPattern";
import type { Scope } from "./types";

interface RuleListItem {
  key: string;
  primary: string;
}

// RuleList renders the editable allow/deny pattern chips for one bucket, with
// a live match count and hover highlight wired back to the explorer. Extracted
// from PersonaEditor.tsx (#766).
export function RuleList({
  bucket,
  patterns,
  items,
  highlightRule,
  onHover,
  onRemove,
}: {
  bucket: "allow" | "deny";
  patterns: string[];
  items: RuleListItem[];
  highlightRule: { bucket: "allow" | "deny"; pattern: string } | null;
  onHover: (p: string | null) => void;
  onRemove: (p: string) => void;
}) {
  if (patterns.length === 0) {
    return (
      <p className="text-[11px] italic text-muted-foreground">No patterns.</p>
    );
  }
  const color =
    bucket === "allow"
      ? "border-emerald-200 bg-emerald-50/40 text-emerald-900 hover:bg-emerald-50 dark:border-emerald-900 dark:bg-emerald-950/20 dark:text-emerald-300 dark:hover:bg-emerald-950/40"
      : "border-rose-200 bg-rose-50/40 text-rose-900 hover:bg-rose-50 dark:border-rose-900 dark:bg-rose-950/20 dark:text-rose-300 dark:hover:bg-rose-950/40";
  return (
    <div className="space-y-1">
      {patterns.map((p) => {
        const matches = items.filter((it) => matchPattern(p, it.primary)).length;
        const isHovered =
          highlightRule?.bucket === bucket && highlightRule.pattern === p;
        return (
          <div
            key={p}
            onMouseEnter={() => onHover(p)}
            onMouseLeave={() => onHover(null)}
            className={cn(
              "group flex items-center gap-2 rounded border px-2 py-1 transition-colors",
              color,
              isHovered && "ring-1 ring-offset-1 ring-offset-background",
            )}
          >
            <span className="flex-1 truncate font-mono text-[11px]">
              {renderPattern(p)}
            </span>
            <span className="font-mono text-[10px] text-muted-foreground">
              {matches}
            </span>
            <button
              onClick={() => onRemove(p)}
              className="rounded p-0.5 opacity-0 transition-opacity hover:bg-background group-hover:opacity-100"
              aria-label={`remove ${p}`}
            >
              <X className="h-3 w-3" />
            </button>
          </div>
        );
      })}
    </div>
  );
}

// AddPatternButton is the collapsible "add pattern" affordance below each rule
// list: a text input with kind-derived presets and a live match preview.
export function AddPatternButton({
  bucket,
  onAdd,
  items,
  existing,
  scope,
}: {
  bucket: "allow" | "deny";
  onAdd: (p: string) => void;
  items: { key: string; primary: string; kind: string }[];
  existing: string[];
  scope: Scope;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState("");

  const preview = useMemo(
    () =>
      draft ? items.filter((it) => matchPattern(draft, it.primary)) : [],
    [draft, items],
  );

  const kinds = useMemo(
    () => Array.from(new Set(items.map((i) => i.kind))).sort(),
    [items],
  );

  return (
    <div className="mt-2">
      {!open ? (
        <button
          type="button"
          onClick={() => setOpen(true)}
          className={cn(
            "flex w-full items-center justify-center gap-1.5 rounded-md border border-dashed py-1.5 text-[11px] transition-colors",
            bucket === "allow"
              ? "border-emerald-300 text-emerald-700 hover:bg-emerald-50 dark:border-emerald-800 dark:text-emerald-400 dark:hover:bg-emerald-950/40"
              : "border-rose-300 text-rose-700 hover:bg-rose-50 dark:border-rose-800 dark:text-rose-400 dark:hover:bg-rose-950/40",
          )}
        >
          <Plus className="h-3 w-3" />
          Add {bucket} pattern
        </button>
      ) : (
        <div
          className={cn(
            "rounded-md border p-2",
            bucket === "allow"
              ? "border-emerald-200 dark:border-emerald-900"
              : "border-rose-200 dark:border-rose-900",
          )}
        >
          <input
            type="text"
            autoFocus
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault();
                if (draft.trim() && !existing.includes(draft.trim())) {
                  onAdd(draft);
                  setDraft("");
                  setOpen(false);
                }
              } else if (e.key === "Escape") {
                setOpen(false);
                setDraft("");
              }
            }}
            placeholder={
              scope === "tools" ? "e.g. trino_* or *_delete_*" : "e.g. acme-*"
            }
            className="w-full rounded border bg-background px-2 py-1 font-mono text-[11px] outline-none ring-ring focus:ring-2"
          />
          <div className="mt-1.5 flex flex-wrap items-center gap-1">
            <span className="text-[10px] text-muted-foreground">presets:</span>
            {kinds.map((k) => (
              <button
                key={k}
                onClick={() => setDraft(`${k}_*`)}
                type="button"
                className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] hover:bg-muted-foreground/10"
              >
                {k}_*
              </button>
            ))}
            <button
              onClick={() => setDraft("*_delete_*")}
              type="button"
              className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] hover:bg-muted-foreground/10"
            >
              *_delete_*
            </button>
            <button
              onClick={() => setDraft("*_get_*")}
              type="button"
              className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] hover:bg-muted-foreground/10"
            >
              *_get_*
            </button>
          </div>
          <div className="mt-2 text-[11px]">
            {draft ? (
              <span className="text-muted-foreground">
                Will{" "}
                <strong
                  className={
                    bucket === "allow"
                      ? "text-emerald-700 dark:text-emerald-400"
                      : "text-rose-700 dark:text-rose-400"
                  }
                >
                  {bucket}
                </strong>{" "}
                {preview.length} {preview.length === 1 ? "item" : "items"}
                {preview.length > 0 && preview.length <= 5 && (
                  <span className="ml-1 font-mono text-[10px]">
                    ({preview.map((p) => p.primary).join(", ")})
                  </span>
                )}
              </span>
            ) : (
              <span className="italic text-muted-foreground">
                Type a pattern or pick a preset
              </span>
            )}
          </div>
          <div className="mt-2 flex justify-end gap-1.5">
            <button
              type="button"
              onClick={() => {
                setOpen(false);
                setDraft("");
              }}
              className="rounded border px-2 py-0.5 text-[11px] hover:bg-muted"
            >
              Cancel
            </button>
            <button
              type="button"
              onClick={() => {
                if (draft.trim() && !existing.includes(draft.trim())) {
                  onAdd(draft);
                  setDraft("");
                  setOpen(false);
                }
              }}
              disabled={!draft.trim()}
              className={cn(
                "rounded px-2 py-0.5 text-[11px] text-white disabled:opacity-50",
                bucket === "allow"
                  ? "bg-emerald-600 hover:bg-emerald-700"
                  : "bg-rose-600 hover:bg-rose-700",
              )}
            >
              Add
            </button>
          </div>
        </div>
      )}
    </div>
  );
}
