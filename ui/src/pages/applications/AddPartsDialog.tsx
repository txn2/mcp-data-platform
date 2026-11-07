import { useState } from "react";
import { Search } from "lucide-react";
import {
  useAddApplicationParts,
  useApplicationCandidates,
  type ApplicationCandidate,
  type ApplicationPartKind,
} from "@/api/portal/hooks/applications";
import { formatRelative } from "@/components/feedback/meta";
import { familyLabel } from "@/components/renderers/registry";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { useDebounced } from "@/lib/useDebounced";
import { PartTile } from "./PartTile";
import { PART_GROUP_LABEL, PART_KINDS, PART_KIND_LABEL } from "./applicationFormat";

// AddPartsDialog adds several parts to an application at once (#2527): a
// search by name across automations, assets and webhook sources the caller
// may add, filtered by kind, each picked with a checkbox. A part already in
// another application is shown and cannot be picked; one already in this
// application is not listed. A part the server refuses keeps the dialog open
// with the refusal beside it; the rest are added.

type KindFilter = ApplicationPartKind | "all";

const keyOf = (c: { kind: string; id: string }) => `${c.kind}/${c.id}`;

export function AddPartsDialog({
  app,
  appName,
  onNavigate,
  onClose,
}: {
  app: string;
  appName: string;
  onNavigate: (path: string) => void;
  onClose: () => void;
}) {
  const [query, setQuery] = useState("");
  const q = useDebounced(query.trim(), 250);
  const [kind, setKind] = useState<KindFilter>("all");
  const [picked, setPicked] = useState<Map<string, ApplicationCandidate>>(new Map());
  const [refused, setRefused] = useState<Map<string, string>>(new Map());
  const { data, isLoading } = useApplicationCandidates(app, q);
  const add = useAddApplicationParts(app);

  const all = data?.candidates ?? [];
  const shown = kind === "all" ? all : all.filter((c) => c.kind === kind);
  const total = PART_KINDS.reduce((n, k) => n + (data?.counts[k] ?? 0), 0);

  const toggle = (c: ApplicationCandidate) =>
    setPicked((prev) => {
      const next = new Map(prev);
      if (next.has(keyOf(c))) next.delete(keyOf(c));
      else next.set(keyOf(c), c);
      return next;
    });

  const submit = () => {
    const parts = [...picked.values()].map((c) => ({ kind: c.kind, id: c.id }));
    add.mutate(parts, {
      onSuccess: (outcomes) => {
        const failed = outcomes.filter((o) => o.error !== null);
        if (failed.length === 0) {
          onClose();
          return;
        }
        setRefused(new Map(failed.map((o) => [keyOf(o), o.error ?? ""])));
        setPicked((prev) => new Map([...prev].filter(([k]) => failed.some((o) => keyOf(o) === k))));
      },
    });
  };

  const count = picked.size;
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="gap-0 p-0 sm:max-w-[660px]">
        <DialogHeader className="px-6 pt-6">
          <DialogTitle>Add parts to {appName}</DialogTitle>
          <DialogDescription>
            A part belongs to one application. The next release you publish pins its current version.
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-3.5 px-6 pt-5">
          <label className="relative block">
            <span className="sr-only">Search parts</span>
            <Search aria-hidden="true" className="absolute top-1/2 left-3.5 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              autoFocus
              type="search"
              className="h-11 pl-10"
              placeholder="Search automations, assets and webhook sources"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </label>
          <div role="tablist" aria-label="Kind" className="flex flex-wrap gap-1.5">
            <KindPill selected={kind === "all"} onClick={() => setKind("all")} label={`All · ${total}`} />
            {PART_KINDS.map((k) => (
              <KindPill
                key={k}
                selected={kind === k}
                onClick={() => setKind(k)}
                label={`${PART_GROUP_LABEL[k]} · ${data?.counts[k] ?? 0}`}
              />
            ))}
          </div>
        </div>
        <ul aria-label="Matching parts" className="mt-4 flex max-h-[470px] flex-col gap-0.5 overflow-auto px-3">
          {isLoading && <li className="px-3 py-6 text-sm text-muted-foreground">Searching…</li>}
          {!isLoading && shown.length === 0 && (
            <li className="px-3 py-6 text-sm text-muted-foreground">
              {q
                ? `No automations, assets or webhook sources match '${q}'`
                : "No automations, assets or webhook sources you can add"}
            </li>
          )}
          {shown.map((c) => (
            <CandidateRow
              key={keyOf(c)}
              candidate={c}
              query={q}
              checked={picked.has(keyOf(c))}
              refusal={refused.get(keyOf(c))}
              onToggle={() => toggle(c)}
              onOpenApp={(name) => {
                onClose();
                onNavigate(`/applications/${encodeURIComponent(name)}`);
              }}
            />
          ))}
        </ul>
        <DialogFooter className="mt-4 flex-row items-center justify-between border-t px-6 py-4 sm:justify-between">
          <span className="text-sm text-muted-foreground">{count} selected</span>
          <div className="flex gap-2.5">
            <Button variant="outline" onClick={onClose} disabled={add.isPending}>
              Cancel
            </Button>
            <Button onClick={submit} disabled={count === 0 || add.isPending}>
              {count === 1 ? "Add 1 part" : `Add ${count} parts`}
            </Button>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function KindPill({ selected, onClick, label }: { selected: boolean; onClick: () => void; label: string }) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={selected}
      onClick={onClick}
      className={cn(
        "h-8 rounded-full border px-3 text-[13px] font-medium",
        selected ? "border-foreground bg-foreground text-background" : "text-muted-foreground hover:bg-muted",
      )}
    >
      {label}
    </button>
  );
}

function CandidateRow({
  candidate: c,
  query,
  checked,
  refusal,
  onToggle,
  onOpenApp,
}: {
  candidate: ApplicationCandidate;
  query: string;
  checked: boolean;
  refusal?: string;
  onToggle: () => void;
  onOpenApp: (name: string) => void;
}) {
  const taken = c.application !== undefined;
  const id = `candidate-${c.kind}-${c.id}`;
  return (
    <li data-testid={id}>
      <div
        className={cn(
          "grid grid-cols-[22px_34px_minmax(0,1fr)] items-center gap-3 rounded-lg p-3",
          checked && "bg-primary/10",
          !taken && "cursor-pointer",
        )}
      >
        <input
          id={id}
          type="checkbox"
          className="size-[18px] accent-primary disabled:opacity-40"
          checked={checked}
          disabled={taken}
          aria-label={taken ? `${c.name}, already in ${c.application?.name}` : c.name}
          onChange={onToggle}
        />
        <PartTile kind={c.kind} muted={taken} size={34} />
        <label htmlFor={id} className={cn("flex min-w-0 flex-col gap-0.5", !taken && "cursor-pointer")}>
          <span className={cn("truncate text-sm font-medium", taken && "text-muted-foreground")}>
            <Highlight text={c.name} query={query} />
          </span>
          {taken ? (
            <span className="text-[13px] text-muted-foreground">
              Already part of{" "}
              <button
                type="button"
                className="text-primary hover:underline"
                onClick={() => onOpenApp(c.application!.name)}
              >
                {c.application!.name}
              </button>
              . Remove it there to add it here.
            </span>
          ) : (
            <span className="truncate text-[13px] text-muted-foreground">{candidateLine(c)}</span>
          )}
          {refusal && <span className="text-[13px] text-destructive">{refusal}</span>}
        </label>
      </div>
    </li>
  );
}

/** candidateLine is the secondary line of a result: its kind and what it is. */
function candidateLine(c: ApplicationCandidate): string {
  const pieces: string[] = [PART_KIND_LABEL[c.kind]];
  if (c.kind === "script") {
    if (c.owner_email) pieces.push(c.owner_email);
    if (c.updated_at) pieces.push(`saved ${formatRelative(c.updated_at)}`);
  } else if (c.kind === "asset") {
    if (c.content_type) pieces.push(familyLabel(c.content_type));
    if (c.updated_at) pieces.push(`updated ${formatRelative(c.updated_at)}`);
  }
  return pieces.join(" · ");
}

/** Highlight bolds the first case-insensitive match of query in text. */
function Highlight({ text, query }: { text: string; query: string }) {
  const at = query ? text.toLowerCase().indexOf(query.toLowerCase()) : -1;
  if (at < 0) return <>{text}</>;
  return (
    <>
      {text.slice(0, at)}
      <strong className="font-bold text-foreground">{text.slice(at, at + query.length)}</strong>
      {text.slice(at + query.length)}
    </>
  );
}
