import { useCallback, useEffect, useState } from "react";
import { createPortal } from "react-dom";
import { Check, FolderPlus, Plus } from "lucide-react";
import {
  useAddToCollection,
  useCollectionTargets,
  useCreateCollection,
} from "@/api/portal/hooks/collections";
import type { CollectionTarget, SectionSummary } from "@/api/portal/types";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { ApiError } from "@/api/portal/client";
import { usePortalAccess } from "@/hooks/usePortalAccess";

/** What was added and where, for the confirmation that follows. */
export interface AddedTo {
  collectionId: string;
  collectionName: string;
  sectionTitle: string;
}

type Step =
  | { kind: "list" }
  | { kind: "section"; target: CollectionTarget }
  | { kind: "new" };

/** A section's name as the picker shows it; a section may have no title. */
export function sectionLabel(title: string): string {
  return title.trim() || "Untitled section";
}

/** The sections of a collection the asset is already in. */
function placements(t: CollectionTarget): SectionSummary[] {
  return t.sections.filter((s) => s.has_asset);
}

/** Whether there is a section of the collection the asset is not yet in. */
function hasRoom(t: CollectionTarget): boolean {
  return t.sections.length === 0 || t.sections.some((s) => !s.has_asset);
}

function errorText(err: unknown): string {
  if (err instanceof ApiError) return err.message;
  return "The asset could not be added. Try again.";
}

/**
 * Puts the asset being viewed into a collection (#2260): the collections the
 * reader can edit, newest first, with the ones that already hold it marked;
 * then the section, when the collection has more than one; or a new collection
 * with the asset as its first item.
 */
export function AddToCollectionDialog({
  assetId,
  open,
  canCreate,
  onOpenChange,
  onAdded,
}: {
  assetId: string;
  open: boolean;
  /** The reader's persona creates collections; New collection is offered. */
  canCreate: boolean;
  onOpenChange: (open: boolean) => void;
  onAdded: (added: AddedTo) => void;
}) {
  const [step, setStep] = useState<Step>({ kind: "list" });
  const [search, setSearch] = useState("");
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) {
      setStep({ kind: "list" });
      setSearch("");
      setError(null);
    }
  }, [open]);

  const add = useAddToCollection();

  const addTo = (target: CollectionTarget, section?: SectionSummary) => {
    setError(null);
    add.mutate(
      { collectionId: target.id, assetId, sectionId: section?.id },
      {
        onSuccess: (res) => {
          onOpenChange(false);
          onAdded({ collectionId: target.id, collectionName: target.name, sectionTitle: res.section_title });
        },
        onError: (err) => setError(errorText(err)),
      },
    );
  };

  const choose = (target: CollectionTarget) => {
    if (target.sections.length > 1) {
      setStep({ kind: "section", target });
      return;
    }
    addTo(target, target.sections[0]);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>
            {step.kind === "section" ? "Choose a section" : step.kind === "new" ? "New collection" : "Add to collection"}
          </DialogTitle>
        </DialogHeader>
        {step.kind === "list" && (
          <CollectionList
            assetId={assetId}
            open={open}
            search={search}
            onSearch={setSearch}
            canCreate={canCreate}
            busy={add.isPending}
            onChoose={choose}
            onNew={() => setStep({ kind: "new" })}
          />
        )}
        {step.kind === "section" && (
          <SectionStep
            target={step.target}
            busy={add.isPending}
            onBack={() => setStep({ kind: "list" })}
            onAdd={(section) => addTo(step.target, section)}
          />
        )}
        {step.kind === "new" && (
          <NewCollectionStep
            assetId={assetId}
            onBack={() => setStep({ kind: "list" })}
            onError={setError}
            onCreated={(id, name) => {
              onOpenChange(false);
              onAdded({ collectionId: id, collectionName: name, sectionTitle: "" });
            }}
          />
        )}
        {error && (
          <p role="alert" className="text-sm text-destructive">
            {error}
          </p>
        )}
      </DialogContent>
    </Dialog>
  );
}

function CollectionList({
  assetId,
  open,
  search,
  onSearch,
  canCreate,
  busy,
  onChoose,
  onNew,
}: {
  assetId: string;
  open: boolean;
  search: string;
  onSearch: (s: string) => void;
  canCreate: boolean;
  busy: boolean;
  onChoose: (t: CollectionTarget) => void;
  onNew: () => void;
}) {
  const { data, isLoading, isError } = useCollectionTargets(assetId, search, open);
  const targets = data?.data ?? [];

  return (
    <div className="flex min-w-0 flex-col gap-3">
      {canCreate && (
        <Button variant="outline" size="sm" className="justify-start" onClick={onNew}>
          <Plus />
          New collection
        </Button>
      )}
      <Input
        type="search"
        placeholder="Search collections"
        aria-label="Search collections"
        value={search}
        onChange={(e) => onSearch(e.target.value)}
      />
      <ul className="max-h-[50vh] min-w-0 overflow-y-auto rounded-md border" aria-label="Collections">
        {isLoading && <li className="p-3 text-sm text-muted-foreground">Loading collections...</li>}
        {isError && <li className="p-3 text-sm text-destructive">The collections could not be loaded.</li>}
        {!isLoading && !isError && targets.length === 0 && (
          <li className="p-3 text-sm text-muted-foreground">
            {search.trim() ? "No collection you can edit matches." : "You have no collections you can edit."}
          </li>
        )}
        {targets.map((t) => {
          const placed = placements(t);
          const room = hasRoom(t);
          return (
            <li key={t.id} className="border-b last:border-b-0">
              <button
                type="button"
                className="flex w-full min-w-0 items-start gap-3 px-3 py-2 text-left hover:bg-muted disabled:cursor-not-allowed disabled:opacity-60 disabled:hover:bg-transparent"
                disabled={busy || !room}
                onClick={() => onChoose(t)}
              >
                <FolderPlus className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
                <span className="min-w-0 flex-1">
                  <span className="block truncate text-sm font-medium">{t.name}</span>
                  <span className="block truncate text-xs text-muted-foreground">
                    {placed.length > 0
                      ? `Already in ${placed.map((s) => sectionLabel(s.title)).join(", ")}`
                      : t.shared && t.owner_email
                        ? `Owned by ${t.owner_email}`
                        : `${t.sections.length} ${t.sections.length === 1 ? "section" : "sections"}`}
                  </span>
                </span>
                {placed.length > 0 && <Check className="mt-0.5 size-4 shrink-0 text-primary" aria-label="Contains this asset" />}
              </button>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function SectionStep({
  target,
  busy,
  onBack,
  onAdd,
}: {
  target: CollectionTarget;
  busy: boolean;
  onBack: () => void;
  onAdd: (s: SectionSummary) => void;
}) {
  // The last section the asset is not already in is the default.
  const open = target.sections.filter((s) => !s.has_asset);
  const [chosen, setChosen] = useState<string>(open[open.length - 1]?.id ?? "");

  return (
    <div className="flex min-w-0 flex-col gap-3">
      <p className="truncate text-sm text-muted-foreground">{target.name}</p>
      <ul className="max-h-[50vh] overflow-y-auto rounded-md border" aria-label="Sections">
        {target.sections.map((s) => (
          <li key={s.id} className="border-b last:border-b-0">
            <label
              className={`flex items-center gap-3 px-3 py-2 text-sm ${s.has_asset ? "opacity-60" : "cursor-pointer hover:bg-muted"}`}
            >
              <input
                type="radio"
                name="section"
                value={s.id}
                checked={chosen === s.id}
                disabled={s.has_asset}
                onChange={() => setChosen(s.id)}
              />
              <span className="min-w-0 flex-1 truncate">{sectionLabel(s.title)}</span>
              <span className="shrink-0 text-xs text-muted-foreground">
                {s.has_asset ? "Already here" : `${s.item_count} ${s.item_count === 1 ? "item" : "items"}`}
              </span>
            </label>
          </li>
        ))}
      </ul>
      <div className="flex justify-end gap-2">
        <Button variant="outline" size="sm" onClick={onBack}>
          Back
        </Button>
        <Button
          size="sm"
          disabled={busy || !chosen}
          onClick={() => {
            const s = target.sections.find((x) => x.id === chosen);
            if (s) onAdd(s);
          }}
        >
          {busy ? "Adding..." : "Add"}
        </Button>
      </div>
    </div>
  );
}

function NewCollectionStep({
  assetId,
  onBack,
  onError,
  onCreated,
}: {
  assetId: string;
  onBack: () => void;
  onError: (msg: string | null) => void;
  onCreated: (id: string, name: string) => void;
}) {
  const [name, setName] = useState("");
  const create = useCreateCollection();
  const submit = () => {
    const trimmed = name.trim();
    if (!trimmed) return;
    onError(null);
    create.mutate(
      { name: trimmed, asset_id: assetId },
      {
        onSuccess: (coll) => onCreated(coll.id, coll.name),
        onError: (err) => onError(errorText(err)),
      },
    );
  };
  return (
    <form
      className="flex flex-col gap-3"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      <Input
        autoFocus
        aria-label="Collection name"
        placeholder="Collection name"
        value={name}
        maxLength={255}
        onChange={(e) => setName(e.target.value)}
      />
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" size="sm" onClick={onBack}>
          Back
        </Button>
        <Button type="submit" size="sm" disabled={create.isPending || !name.trim()}>
          {create.isPending ? "Creating..." : "Create and add"}
        </Button>
      </div>
    </form>
  );
}

/** The confirmation after an add, linking to the collection. */
export function AddedToast({
  added,
  onOpen,
  onDismiss,
}: {
  added: AddedTo | null;
  onOpen: (collectionId: string) => void;
  onDismiss: () => void;
}) {
  useEffect(() => {
    if (!added) return;
    const t = setTimeout(onDismiss, 6000);
    return () => clearTimeout(t);
  }, [added, onDismiss]);
  if (!added) return null;
  const where = added.sectionTitle.trim()
    ? `${added.collectionName} / ${added.sectionTitle}`
    : added.collectionName;
  return createPortal(
    <div
      role="status"
      data-testid="added-to-collection"
      className="fixed bottom-6 left-1/2 z-[60] flex max-w-[calc(100vw-2rem)] -translate-x-1/2 items-center gap-3.5 rounded-lg bg-foreground px-3.5 py-2 text-[13px] text-background shadow-lg"
    >
      <span className="min-w-0 truncate">Added to {where}</span>
      <button
        type="button"
        className="shrink-0 font-semibold text-[hsl(217_91%_65%)] underline-offset-2 hover:underline dark:text-primary"
        onClick={() => {
          onDismiss();
          onOpen(added.collectionId);
        }}
      >
        Open
      </button>
    </div>,
    document.body,
  );
}

/** The viewer's add-to-collection state: whether it is offered, and the dialog
 * and confirmation it opens. */
export interface AddToCollectionAction {
  /** Opens the picker; absent when the action is not offered. */
  onOpen?: () => void;
  canCreate: boolean;
  open: boolean;
  setOpen: (open: boolean) => void;
  added: AddedTo | null;
  setAdded: (a: AddedTo | null) => void;
}

/**
 * Whether the viewer offers Add to collection (#2260): to a persona that
 * creates collections, which always has New collection, and otherwise only
 * when there is a collection the reader can edit.
 */
export function useAddToCollectionAction(assetId: string | undefined, collectable: boolean): AddToCollectionAction {
  const portal = usePortalAccess();
  const [open, setOpen] = useState(false);
  const [added, setAdded] = useState<AddedTo | null>(null);
  const canCreate = portal.assets === "own";
  const { data: editable } = useCollectionTargets(assetId ?? "", "", collectable && !canCreate);
  const offered = collectable && !!assetId && (canCreate || (editable?.data.length ?? 0) > 0);
  const onOpen = useCallback(() => setOpen(true), []);
  return { onOpen: offered ? onOpen : undefined, canCreate, open, setOpen, added, setAdded };
}

/** The picker and the confirmation, for the viewer to render beside its other dialogs. */
export function AddToCollection({
  action,
  assetId,
  onNavigate,
}: {
  action: AddToCollectionAction;
  assetId: string;
  onNavigate: (path: string) => void;
}) {
  const { setAdded } = action;
  const dismiss = useCallback(() => setAdded(null), [setAdded]);
  return (
    <>
      {action.onOpen && (
        <AddToCollectionDialog
          assetId={assetId}
          open={action.open}
          canCreate={action.canCreate}
          onOpenChange={action.setOpen}
          onAdded={setAdded}
        />
      )}
      <AddedToast
        added={action.added}
        onDismiss={dismiss}
        onOpen={(id) => onNavigate(`/collections/${encodeURIComponent(id)}`)}
      />
    </>
  );
}
