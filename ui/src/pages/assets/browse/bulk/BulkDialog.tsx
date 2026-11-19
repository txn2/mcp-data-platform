import { useId, useState } from "react";
import { useCollectionTargets } from "@/api/portal/hooks/collections";
import { useSharePersonas } from "@/api/portal/hooks/assets";
import type { BulkRequest } from "@/api/portal/hooks/bulk";
import { TagSuggestInput, splitTags } from "@/components/assetviewer/TagSuggestInput";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import type { BulkDialogKind } from "./BulkBar";

/** What a dialog asks to apply: the request without the ids. */
export type BulkArgs = Omit<BulkRequest, "asset_ids">;

const statuses = [
  { value: "draft", label: "Draft" },
  { value: "published", label: "Published" },
  { value: "deprecated", label: "Deprecated" },
] as const;

const titles: Record<BulkDialogKind, string> = {
  add_tags: "Add tags to",
  remove_tags: "Remove tags from",
  add_to_collection: "Add to a collection:",
  status: "Set the status of",
  share: "Share",
  delete: "Delete",
};

function plural(n: number): string {
  return `${n} ${n === 1 ? "asset" : "assets"}`;
}

/**
 * BulkDialog asks for what one bulk action needs, for the assets selected
 * (#2507): the tags, the collection and section, the status, the recipient,
 * or a confirmation. Each action applies only to the assets the reader may
 * perform it on; the rest are listed afterwards with the reason.
 */
export function BulkDialog({
  kind,
  count,
  firstId,
  busy,
  onApply,
  onClose,
}: {
  kind: BulkDialogKind | null;
  count: number;
  /** One selected asset, which the collection list is read against. */
  firstId: string;
  busy: boolean;
  onApply: (args: BulkArgs) => void;
  onClose: () => void;
}) {
  return (
    <Dialog open={kind !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="sm:max-w-md">
        {kind && (
          <>
            <DialogHeader>
              <DialogTitle>
                {titles[kind]} {plural(count)}
              </DialogTitle>
              <DialogDescription>
                Applied only to the assets you may change. The rest are listed afterwards with the reason.
              </DialogDescription>
            </DialogHeader>
            <DialogBody kind={kind} firstId={firstId} busy={busy} onApply={onApply} onClose={onClose} />
          </>
        )}
      </DialogContent>
    </Dialog>
  );
}

function DialogBody(props: {
  kind: BulkDialogKind;
  firstId: string;
  busy: boolean;
  onApply: (args: BulkArgs) => void;
  onClose: () => void;
}) {
  switch (props.kind) {
    case "add_tags":
    case "remove_tags":
      return <TagsForm {...props} action={props.kind} />;
    case "add_to_collection":
      return <CollectionForm {...props} />;
    case "status":
      return <StatusForm {...props} />;
    case "share":
      return <ShareForm {...props} />;
    default:
      return <DeleteForm {...props} />;
  }
}

function Actions({
  label,
  disabled,
  busy,
  danger,
  onApply,
  onClose,
}: {
  label: string;
  disabled?: boolean;
  busy: boolean;
  danger?: boolean;
  onApply: () => void;
  onClose: () => void;
}) {
  return (
    <div className="flex justify-end gap-2 pt-2">
      <Button variant="secondary" onClick={onClose}>
        Cancel
      </Button>
      <Button variant={danger ? "destructive" : "default"} disabled={disabled || busy} onClick={onApply}>
        {busy ? "Working..." : label}
      </Button>
    </div>
  );
}

function TagsForm({
  action,
  busy,
  onApply,
  onClose,
}: {
  action: "add_tags" | "remove_tags";
  busy: boolean;
  onApply: (args: BulkArgs) => void;
  onClose: () => void;
}) {
  const id = useId();
  const [value, setValue] = useState("");
  const tags = splitTags(value);
  return (
    <div className="space-y-2">
      <Label htmlFor={id}>Tags (comma-separated)</Label>
      <TagSuggestInput id={id} value={value} onChange={setValue} autoFocus />
      <Actions
        label={action === "add_tags" ? "Add tags" : "Remove tags"}
        disabled={tags.length === 0}
        busy={busy}
        onApply={() => onApply({ action, tags })}
        onClose={onClose}
      />
    </div>
  );
}

function CollectionForm({
  firstId,
  busy,
  onApply,
  onClose,
}: {
  firstId: string;
  busy: boolean;
  onApply: (args: BulkArgs) => void;
  onClose: () => void;
}) {
  const [search, setSearch] = useState("");
  const [collectionId, setCollectionId] = useState("");
  const [sectionId, setSectionId] = useState("");
  const { data, isLoading } = useCollectionTargets(firstId, search, true);
  const targets = data?.data ?? [];
  const chosen = targets.find((t) => t.id === collectionId);
  return (
    <div className="space-y-3">
      <Input
        aria-label="Find a collection"
        placeholder="Find a collection"
        value={search}
        onChange={(e) => setSearch(e.target.value)}
      />
      {isLoading && <p className="text-sm text-muted-foreground">Loading...</p>}
      {!isLoading && targets.length === 0 && (
        <p className="text-sm text-muted-foreground">No collection you can edit matches.</p>
      )}
      <ul className="max-h-56 space-y-1 overflow-auto" aria-label="Collections">
        {targets.map((t) => (
          <li key={t.id}>
            <button
              type="button"
              aria-pressed={t.id === collectionId}
              className={`min-h-11 w-full rounded-md border px-3 text-left text-sm sm:min-h-9 ${t.id === collectionId ? "border-primary bg-primary/5" : ""}`}
              onClick={() => {
                setCollectionId(t.id);
                setSectionId("");
              }}
            >
              {t.name}
            </button>
          </li>
        ))}
      </ul>
      {chosen && chosen.sections.length > 1 && (
        <label className="flex flex-col gap-1.5 text-sm">
          Section
          <select
            className="h-11 rounded-md border bg-background px-2 sm:h-9"
            value={sectionId}
            onChange={(e) => setSectionId(e.target.value)}
          >
            <option value="">The last section</option>
            {chosen.sections.map((s) => (
              <option key={s.id} value={s.id}>
                {s.title.trim() || "Untitled section"}
              </option>
            ))}
          </select>
        </label>
      )}
      <Actions
        label="Add to collection"
        disabled={!collectionId}
        busy={busy}
        onApply={() =>
          onApply({ action: "add_to_collection", collection_id: collectionId, section_id: sectionId || undefined })
        }
        onClose={onClose}
      />
    </div>
  );
}

function StatusForm({
  busy,
  onApply,
  onClose,
}: {
  busy: boolean;
  onApply: (args: BulkArgs) => void;
  onClose: () => void;
}) {
  const id = useId();
  const [status, setStatus] = useState("");
  const [note, setNote] = useState("");
  return (
    <div className="space-y-3">
      <fieldset className="space-y-1.5">
        <legend className="mb-1 text-sm">Status</legend>
        {statuses.map((s) => (
          <label key={s.value} className="flex min-h-11 items-center gap-2 text-sm sm:min-h-8">
            <input type="radio" name={`${id}-status`} checked={status === s.value} onChange={() => setStatus(s.value)} />
            {s.label}
          </label>
        ))}
      </fieldset>
      {status === "deprecated" && (
        <div className="space-y-1.5">
          <Label htmlFor={`${id}-note`}>Note readers see (optional)</Label>
          <Textarea id={`${id}-note`} rows={2} value={note} onChange={(e) => setNote(e.target.value)} />
        </div>
      )}
      <p className="text-xs text-muted-foreground">A status is informational: it changes nobody&rsquo;s access.</p>
      <div className="flex flex-wrap justify-between gap-2 pt-2">
        <Button variant="ghost" disabled={busy} onClick={() => onApply({ action: "clear_status" })}>
          Clear status
        </Button>
        <Actions
          label="Set status"
          disabled={!status}
          busy={busy}
          onApply={() => onApply({ action: "set_status", status, note: status === "deprecated" ? note : undefined })}
          onClose={onClose}
        />
      </div>
    </div>
  );
}

function ShareForm({
  busy,
  onApply,
  onClose,
}: {
  busy: boolean;
  onApply: (args: BulkArgs) => void;
  onClose: () => void;
}) {
  const id = useId();
  const [who, setWho] = useState("");
  const [permission, setPermission] = useState<"viewer" | "editor">("viewer");
  const { data } = useSharePersonas();
  const personas = data?.personas ?? [];
  const recipient = who.trim();
  const isPerson = recipient.includes("@");
  return (
    <div className="space-y-3">
      <div className="space-y-1.5">
        <Label htmlFor={`${id}-who`}>With a person or persona</Label>
        <Input
          id={`${id}-who`}
          list={`${id}-personas`}
          value={who}
          placeholder="name@example.com or a persona"
          onChange={(e) => setWho(e.target.value)}
        />
        <datalist id={`${id}-personas`}>
          {personas.map((p) => (
            <option key={p.name} value={p.name}>
              {p.display_name}
            </option>
          ))}
        </datalist>
      </div>
      <label className="flex flex-col gap-1.5 text-sm">
        Permission
        <select
          className="h-11 rounded-md border bg-background px-2 sm:h-9"
          value={permission}
          onChange={(e) => setPermission(e.target.value as "viewer" | "editor")}
        >
          <option value="viewer">Can view</option>
          <option value="editor">Can edit</option>
        </select>
      </label>
      <p className="text-xs text-muted-foreground">A bulk share sends no email.</p>
      <Actions
        label="Share"
        disabled={!recipient}
        busy={busy}
        onApply={() =>
          onApply({
            action: "share",
            share: isPerson
              ? { shared_with_email: recipient, permission }
              : { shared_with_persona: recipient, permission },
          })
        }
        onClose={onClose}
      />
    </div>
  );
}

function DeleteForm({
  busy,
  onApply,
  onClose,
}: {
  busy: boolean;
  onApply: (args: BulkArgs) => void;
  onClose: () => void;
}) {
  return (
    <div className="space-y-3">
      <p className="text-sm">
        The assets you own go to Trash, where they can be restored until they are purged. Shared assets you do not
        own are left alone.
      </p>
      <Actions label="Delete" danger busy={busy} onApply={() => onApply({ action: "delete" })} onClose={onClose} />
    </div>
  );
}
