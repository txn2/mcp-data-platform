import { useState } from "react";
import { BookMarked, Folder } from "lucide-react";
import {
  useDefinition,
  useDeleteDefinition,
  type DefinitionKind,
  type DefinitionView,
} from "@/api/portal/catalog";
import { KnowledgeBacklinks } from "@/components/knowledge/KnowledgeBacklinks";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { CreateDefinitionForm } from "./CreateDefinitionForm";
import { BackToList, DeleteControl, EntityDescription, UseLink, VocabCard } from "./governance";
import { HistoryPanel } from "./HistoryPanel";
import { AddButton, ListSkeleton } from "./primitives";
import { KIND_NAMES } from "./utils";

// DELETE_IMPACT says what deleting each kind does. The platform refuses a
// delete of a definition still in use, so none of these strips anything.
const DELETE_IMPACT: Record<DefinitionKind, string> = {
  glossary_term:
    "Deleting this term removes it from the glossary. A term still applied to a dataset or column is refused; remove it from those first.",
  glossary_node:
    "Deleting this node removes it from the glossary. A node with entries under it is refused; move or delete them first.",
  tag: "Deleting this tag removes it from the tag list. A tag still on a dataset or column is refused; remove it from those first.",
  domain:
    "Deleting this domain removes it from the domain list. A domain still set on a dataset is refused; clear it there first.",
  data_product: "Deleting this data product removes it from the catalog.",
};

/**
 * DefinitionDetail is one definition's page: what it means, where it sits in
 * the glossary, what is under it, which datasets and columns carry it, and the
 * history of its description. The same page serves every kind, since they are
 * curated through the same writes.
 */
export function DefinitionDetail({
  reference,
  backLabel,
  onBack,
  onOpen,
  onNavigate,
}: {
  reference: string;
  backLabel: string;
  onBack: () => void;
  // onOpen opens another definition in the same tab (a glossary entry's
  // parent or child).
  onOpen?: (ref: string) => void;
  onNavigate?: (path: string) => void;
}) {
  const { data, isLoading, isError } = useDefinition(reference);
  return (
    <div className="space-y-4">
      <BackToList label={backLabel} onBack={onBack} />
      {data ? (
        <DefinitionBody def={data} onBack={onBack} onOpen={onOpen} onNavigate={onNavigate} />
      ) : isLoading ? (
        <ListSkeleton />
      ) : isError ? (
        <EmptyState>
          No definition answers <span className="font-mono text-xs break-all">{reference}</span>. It
          may have been deleted since it was linked.
        </EmptyState>
      ) : null}
    </div>
  );
}

function DefinitionBody({
  def,
  onBack,
  onOpen,
  onNavigate,
}: {
  def: DefinitionView;
  onBack: () => void;
  onOpen?: (ref: string) => void;
  onNavigate?: (path: string) => void;
}) {
  const del = useDeleteDefinition();
  const kindName = KIND_NAMES[def.kind];
  const carried = def.kind === "glossary_term" || def.kind === "tag" || def.kind === "domain";
  return (
    <div className="space-y-6">
      <DefinitionHeader def={def} onOpen={onOpen} />

      <EntityDescription
        reference={def.reference}
        value={def.aspects?.description ?? ""}
        canEdit={def.writable}
        label={`Description of ${def.name}`}
      />

      {def.kind === "glossary_node" && <EntriesSection def={def} onOpen={onOpen} />}

      {carried && <UsesSection def={def} onNavigate={onNavigate} />}

      <KnowledgeBacklinks urn={def.reference} onNavigate={onNavigate} />

      <HistoryPanel reference={def.reference} canEdit={def.writable} />

      {def.writable && (
        <DeleteControl
          label={`Delete ${kindName}`}
          impact={DELETE_IMPACT[def.kind]}
          mut={del}
          onConfirm={() => del.mutate(def.reference, { onSuccess: onBack })}
        />
      )}
    </div>
  );
}

function DefinitionHeader({ def, onOpen }: { def: DefinitionView; onOpen?: (ref: string) => void }) {
  const parent = def.parent;
  return (
    <div className="space-y-1">
      <h2 className="text-lg font-semibold">{def.name}</h2>
      <p className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
        <Badge variant="muted">{KIND_NAMES[def.kind]}</Badge>
        {parent && onOpen && (
          <button className="text-primary hover:underline" onClick={() => onOpen(parent)}>
            Up to the parent node
          </button>
        )}
        <code className="break-all">{def.reference}</code>
      </p>
    </div>
  );
}

// EntriesSection is what is filed under a glossary node, and, for an editor,
// the form that files a new entry there.
function EntriesSection({ def, onOpen }: { def: DefinitionView; onOpen?: (ref: string) => void }) {
  const [adding, setAdding] = useState(false);
  return (
    <SectionCard
      title="Entries"
      action={def.writable && !adding && <AddButton label="Add entry" onClick={() => setAdding(true)} />}
    >
      {adding && (
        <div className="mb-3">
          <CreateDefinitionForm
            kinds={["glossary_term", "glossary_node"]}
            parent={def.reference}
            onDone={() => setAdding(false)}
            onCancel={() => setAdding(false)}
          />
        </div>
      )}
      {def.children.length === 0 ? (
        <p className="text-sm text-muted-foreground">Nothing is filed under this node yet.</p>
      ) : (
        <ul className="grid gap-2 sm:grid-cols-2">
          {def.children.map((c) => (
            <li key={c.reference}>
              <VocabCard
                entry={c}
                icon={c.kind === "glossary_node" ? Folder : BookMarked}
                onOpen={() => onOpen?.(c.reference)}
              />
            </li>
          ))}
        </ul>
      )}
    </SectionCard>
  );
}

// UsesSection is the datasets and columns carrying a term, tag or domain, and
// how many more sit on connections the reader's persona does not reach.
function UsesSection({ def, onNavigate }: { def: DefinitionView; onNavigate?: (path: string) => void }) {
  return (
    <SectionCard title="Used by">
      {def.uses.length === 0 ? (
        <p className="text-sm text-muted-foreground">
          No dataset or column carries this {KIND_NAMES[def.kind]}.
        </p>
      ) : (
        <ul className="grid gap-2 sm:grid-cols-2">
          {def.uses.map((u) => (
            <li key={u.reference}>
              <UseLink use={u} onNavigate={onNavigate} />
            </li>
          ))}
        </ul>
      )}
      {def.more_uses && (
        <p className="mt-2 text-xs text-muted-foreground">
          More datasets and columns carry this {KIND_NAMES[def.kind]} than are listed here.
        </p>
      )}
      {def.uses_withheld > 0 && (
        <p className="mt-2 text-xs text-muted-foreground">
          {def.uses_withheld} more on connections your persona does not reach.
        </p>
      )}
    </SectionCard>
  );
}
