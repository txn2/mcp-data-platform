import { useState, useMemo, useCallback } from "react";
import { Plus } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { matchPattern } from "./resolve";
import { BUCKET_TINT, type Bucket } from "./tints";
import type { Scope } from "./types";

export interface PatternItem {
  key: string;
  primary: string;
  kind: string;
}

// PRESET_PATTERNS are the two cross-kind globs worth one click. The per-kind
// globs are derived from the items themselves.
const PRESET_PATTERNS = ["*_delete_*", "*_get_*"];

function PresetRow({
  kinds,
  onPick,
}: {
  kinds: string[];
  onPick: (pattern: string) => void;
}) {
  return (
    <div className="mt-1.5 flex flex-wrap items-center gap-1">
      <span className="text-[10px] text-muted-foreground">presets:</span>
      {[...kinds.map((k) => `${k}_*`), ...PRESET_PATTERNS].map((pattern) => (
        <Button
          key={pattern}
          type="button"
          variant="secondary"
          size="xs"
          onClick={() => onPick(pattern)}
          className="h-5 px-1.5 font-mono text-[10px]"
        >
          {pattern}
        </Button>
      ))}
    </div>
  );
}

function MatchPreview({
  bucket,
  draft,
  preview,
}: {
  bucket: Bucket;
  draft: string;
  preview: PatternItem[];
}) {
  if (!draft) {
    return (
      <span className="italic text-muted-foreground">
        Type a pattern or pick a preset
      </span>
    );
  }
  return (
    <span className="text-muted-foreground">
      Will <strong className={BUCKET_TINT[bucket].text}>{bucket}</strong>{" "}
      {preview.length} {preview.length === 1 ? "item" : "items"}
      {preview.length > 0 && preview.length <= 5 && (
        <span className="ml-1 font-mono text-[10px]">
          ({preview.map((p) => p.primary).join(", ")})
        </span>
      )}
    </span>
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
  bucket: Bucket;
  onAdd: (p: string) => void;
  items: PatternItem[];
  existing: string[];
  scope: Scope;
}) {
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState("");

  const preview = useMemo(
    () => (draft ? items.filter((it) => matchPattern(draft, it.primary)) : []),
    [draft, items],
  );

  const kinds = useMemo(
    () => Array.from(new Set(items.map((i) => i.kind))).sort(),
    [items],
  );

  const close = useCallback(() => {
    setOpen(false);
    setDraft("");
  }, []);

  const commit = useCallback(() => {
    const value = draft.trim();
    if (!value || existing.includes(value)) return;
    onAdd(value);
    close();
  }, [draft, existing, onAdd, close]);

  if (!open) {
    return (
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
        className={cn("mt-2 w-full text-[11px]", BUCKET_TINT[bucket].text)}
      >
        <Plus />
        Add {bucket} pattern
      </Button>
    );
  }

  return (
    <div className={cn("mt-2 rounded-md border p-2", BUCKET_TINT[bucket].border)}>
      <Input
        type="text"
        autoFocus
        value={draft}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            commit();
          } else if (e.key === "Escape") {
            close();
          }
        }}
        placeholder={
          scope === "tools" ? "e.g. trino_* or *_delete_*" : "e.g. acme-*"
        }
        aria-label={`${bucket} pattern`}
        className="h-7 px-2 font-mono text-[11px]"
      />
      <PresetRow kinds={kinds} onPick={setDraft} />
      <div className="mt-2 text-[11px]">
        <MatchPreview bucket={bucket} draft={draft} preview={preview} />
      </div>
      <div className="mt-2 flex justify-end gap-1.5">
        <Button type="button" variant="outline" size="xs" onClick={close}>
          Cancel
        </Button>
        <Button
          type="button"
          size="xs"
          onClick={commit}
          disabled={!draft.trim()}
          className={BUCKET_TINT[bucket].solid}
        >
          Add
        </Button>
      </div>
    </div>
  );
}
