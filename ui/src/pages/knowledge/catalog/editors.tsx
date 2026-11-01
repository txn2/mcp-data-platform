import { useState } from "react";
import { X } from "lucide-react";
import {
  useDefinitions,
  useWriteAspect,
  type AspectName,
  type DefinitionKind,
} from "@/api/portal/catalog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { useDebounced } from "@/lib/useDebounced";
import { MetadataPicker, type Candidate } from "./MetadataPicker";
import { AddButton, MutationError } from "./primitives";
import { refLabel } from "./utils";
import { compareNames } from "@/lib/sortByName";

// The editors for one aspect of a dataset or a column. Each writes the whole
// aspect value through the one aspect write, so the history records exactly
// what the aspect held before and after.

/** Chip is one value a chip editor shows: the key stored and its name. */
interface Chip {
  key: string;
  label: string;
}

function ChipList({
  chips,
  canEdit,
  pending,
  onRemove,
  what,
}: {
  chips: Chip[];
  canEdit: boolean;
  pending: boolean;
  onRemove: (key: string) => void;
  what: string;
}) {
  return (
    <div className="flex flex-wrap items-center gap-1.5">
      {chips.length === 0 && <span className="text-sm text-muted-foreground">None.</span>}
      {chips.map((c) => (
        <span
          key={c.key}
          className="inline-flex items-center gap-1 rounded-full bg-primary/10 px-2 py-0.5 text-xs text-primary"
        >
          {c.label}
          {canEdit && (
            <button
              aria-label={`Remove ${what} ${c.label}`}
              disabled={pending}
              onClick={() => onRemove(c.key)}
              className="rounded-full hover:bg-primary/20"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </span>
      ))}
    </div>
  );
}

/**
 * DefinitionChips edits a list aspect whose entries are definitions: a
 * dataset's or column's tags (stored by name) or glossary terms (stored by
 * reference). The picker searches the definitions of the kind; for tags a typed
 * name no tag has yet is offered too, since a tag is its name.
 */
export function DefinitionChips({
  reference,
  aspect,
  kind,
  read,
  names,
  canEdit,
  what,
}: {
  reference: string;
  aspect: Extract<AspectName, "tags" | "glossary_terms">;
  kind: Extract<DefinitionKind, "tag" | "glossary_term">;
  // read is the aspect as the page read it: undefined when absent.
  read: string[] | undefined;
  names?: Record<string, string>;
  canEdit: boolean;
  what: string;
}) {
  const write = useWriteAspect();
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 250);
  const lookup = useDefinitions({ kind: [kind], q: debounced.trim(), limit: 20 }, canEdit);
  const byName = kind === "tag";
  const values = read ?? [];
  const existing = new Set(values);

  const candidates: Candidate[] = (lookup.data ?? []).map((d) => ({
    key: byName ? d.name : d.reference,
    label: d.name,
  }));
  const typed = query.trim();
  if (byName && typed && !candidates.some((c) => c.key === typed)) {
    candidates.unshift({ key: typed, label: typed, detail: "new tag" });
  }

  const save = (next: string[]) =>
    write.mutate(
      { reference, aspect, value: next.length === 0 ? null : next, expect: read ?? null },
      { onSuccess: () => setQuery("") },
    );

  const chips = values.map((v) => ({
    key: v,
    label: byName ? v : (names?.[v] ?? refLabel(v)),
  }));

  return (
    <div>
      <ChipList
        chips={chips}
        canEdit={canEdit}
        pending={write.isPending}
        what={what}
        onRemove={(key) => save(values.filter((v) => v !== key))}
      />
      {canEdit && (
        <MetadataPicker
          label={`Add ${what}`}
          placeholder={kind === "tag" ? "Search tags by name…" : "Search glossary terms by name…"}
          query={query}
          setQuery={setQuery}
          candidates={candidates}
          loading={lookup.isFetching}
          isPending={write.isPending}
          existingKeys={existing}
          onPick={(c) => !existing.has(c.key) && save([...values, c.key])}
          emptyHint={lookup.isError ? "Lookup failed." : "No matches."}
        />
      )}
      <MutationError mut={write} />
    </div>
  );
}

/** DomainEditor sets or clears a dataset's domain, picked from the domains. */
export function DomainEditor({
  reference,
  value,
  names,
  canEdit,
}: {
  reference: string;
  value: string;
  names: Record<string, string>;
  canEdit: boolean;
}) {
  const write = useWriteAspect();
  const [query, setQuery] = useState("");
  const debounced = useDebounced(query, 250);
  const lookup = useDefinitions({ kind: ["domain"], q: debounced.trim(), limit: 50 }, canEdit);
  const candidates: Candidate[] = (lookup.data ?? []).map((d) => ({ key: d.reference, label: d.name }));

  return (
    <div>
      <div className="flex items-center gap-2 text-sm">
        {value ? (
          <>
            <span>{names[value] ?? refLabel(value)}</span>
            {canEdit && (
              <button
                disabled={write.isPending}
                onClick={() => write.mutate({ reference, aspect: "domain", value: null, expect: value || null })}
                className="text-xs text-muted-foreground hover:text-destructive"
              >
                Clear
              </button>
            )}
          </>
        ) : (
          <span className="text-muted-foreground">None.</span>
        )}
      </div>
      {canEdit && (
        <MetadataPicker
          label="Set domain"
          placeholder="Search domains by name…"
          query={query}
          setQuery={setQuery}
          candidates={candidates}
          loading={lookup.isFetching}
          isPending={write.isPending}
          existingKeys={new Set(value ? [value] : [])}
          onPick={(c) =>
            write.mutate(
              { reference, aspect: "domain", value: c.key, expect: value || null },
              { onSuccess: () => setQuery("") },
            )
          }
          openOnFocus
          emptyHint={lookup.isError ? "Failed to load domains." : "No domains match."}
        />
      )}
      <MutationError mut={write} />
    </div>
  );
}

const OWNER_TYPES = ["technical_owner", "business_owner", "data_steward"];

/** OwnersEditor lists a dataset's owners and adds or removes one. An owner is
 * whatever identifies them to the organization, an email most often. */
export function OwnersEditor({
  reference,
  read,
  canEdit,
}: {
  reference: string;
  read: { owner: string; type?: string }[] | undefined;
  canEdit: boolean;
}) {
  const write = useWriteAspect();
  const owners = read ?? [];
  const [owner, setOwner] = useState("");
  const [type, setType] = useState(OWNER_TYPES[0] ?? "");
  const save = (next: { owner: string; type?: string }[]) =>
    write.mutate(
      { reference, aspect: "owners", value: next.length === 0 ? null : next, expect: read ?? null },
      { onSuccess: () => setOwner("") },
    );

  return (
    <div className="space-y-2">
      <div className="space-y-1">
        {owners.length === 0 && <span className="text-sm text-muted-foreground">None.</span>}
        {owners.map((o) => (
          <div key={`${o.owner}/${o.type ?? ""}`} className="flex items-center gap-2 text-sm">
            <span>{o.owner}</span>
            {o.type && (
              <span className="rounded bg-muted px-1.5 py-0.5 text-[11px] text-muted-foreground">
                {o.type.replace(/_/g, " ")}
              </span>
            )}
            {canEdit && (
              <button
                aria-label={`Remove owner ${o.owner}`}
                disabled={write.isPending}
                onClick={() => save(owners.filter((x) => x !== o))}
                className="text-muted-foreground hover:text-destructive"
              >
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
        ))}
      </div>
      {canEdit && (
        <div className="flex flex-wrap items-center gap-2">
          <Input
            aria-label="Owner"
            value={owner}
            onChange={(e) => setOwner(e.target.value)}
            placeholder="alice@example.com"
            className="h-8 w-64 text-xs"
          />
          <Select value={type} onValueChange={setType}>
            <SelectTrigger size="sm" aria-label="Owner type" className="text-xs">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {OWNER_TYPES.map((t) => (
                <SelectItem key={t} value={t}>
                  {t.replace(/_/g, " ")}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <AddButton
            disabled={write.isPending || !owner.trim()}
            onClick={() => save([...owners, { owner: owner.trim(), type }])}
          />
        </div>
      )}
      <MutationError mut={write} />
    </div>
  );
}

/** PropertiesEditor lists a dataset's custom properties and sets or removes
 * one. */
export function PropertiesEditor({
  reference,
  read,
  canEdit,
}: {
  reference: string;
  read: Record<string, string> | undefined;
  canEdit: boolean;
}) {
  const write = useWriteAspect();
  const properties = read ?? {};
  const [key, setKey] = useState("");
  const [value, setValue] = useState("");
  const entries = Object.entries(properties).sort(([a], [b]) => compareNames(a, b));
  const save = (next: Record<string, string>) =>
    write.mutate(
      {
        reference,
        aspect: "custom_properties",
        value: Object.keys(next).length === 0 ? null : next,
        expect: read ?? null,
      },
      {
        onSuccess: () => {
          setKey("");
          setValue("");
        },
      },
    );

  return (
    <div className="space-y-2">
      {entries.length === 0 ? (
        <span className="text-sm text-muted-foreground">None.</span>
      ) : (
        <dl className="grid grid-cols-[auto_1fr_auto] gap-x-3 gap-y-1 text-sm">
          {entries.map(([k, v]) => (
            <div key={k} className="contents">
              <dt className="font-mono text-xs text-muted-foreground">{k}</dt>
              <dd className="break-words">{v}</dd>
              <span>
                {canEdit && (
                  <button
                    aria-label={`Remove property ${k}`}
                    disabled={write.isPending}
                    onClick={() => {
                      const next = { ...properties };
                      delete next[k];
                      save(next);
                    }}
                    className="text-muted-foreground hover:text-destructive"
                  >
                    <X className="h-3.5 w-3.5" />
                  </button>
                )}
              </span>
            </div>
          ))}
        </dl>
      )}
      {canEdit && (
        <div className="flex flex-wrap items-center gap-2">
          <Input
            aria-label="Property name"
            value={key}
            onChange={(e) => setKey(e.target.value)}
            placeholder="name"
            className="h-8 w-40 font-mono text-xs"
          />
          <Input
            aria-label="Property value"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            placeholder="value"
            className="h-8 w-56 text-xs"
          />
          <AddButton
            label="Set"
            disabled={write.isPending || !key.trim()}
            onClick={() => save({ ...properties, [key.trim()]: value })}
          />
        </div>
      )}
      <MutationError mut={write} />
    </div>
  );
}
