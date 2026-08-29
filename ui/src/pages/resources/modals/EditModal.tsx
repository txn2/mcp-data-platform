import { useCallback, useId, useMemo, useState } from "react";
import { X, Loader2 } from "lucide-react";
import { useResources, useUpdateResource } from "@/api/resources/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { parseTags } from "@/lib/tags";
import type { Resource, ResourceUpdate } from "@/api/resources/types";
import { usePersonas } from "@/api/admin/hooks";
import { useAuthStore } from "@/stores/auth";
import { ModalShell } from "@/components/ModalShell";
import { PathField } from "../parts/PathField";
import { pathProblem } from "../parts/pathRules";
import { everyFolder } from "../parts/tree";
import { LibraryField } from "./LibraryField";
import {
  libraryOptions,
  targetKey,
  PERSON_TARGET,
  type MoveTarget,
  type ScopeTarget,
} from "../scopes";

/**
 * moveFields resolves the library the picker holds into the two fields a PATCH
 * carries, or into the sentence to show instead.
 *
 * It sits outside the component because it is the one place the person-library
 * option turns into an address, and because a picked option that is not in the
 * list is a bug in the picker rather than a state the form can be left in.
 */
function moveFields(
  to: MoveTarget | undefined,
  personEmail: string,
): Pick<ResourceUpdate, "scope" | "scope_id"> | { error: string } {
  if (!to) return { error: "That library is no longer available." };
  if (to.scope_id !== PERSON_TARGET) return { scope: to.scope, scope_id: to.scope_id };
  const address = personEmail.trim();
  if (address === "") return { error: "Name the person whose library this moves to." };
  return { scope: to.scope, scope_id: address };
}

/**
 * How much of a library is read to suggest its folders.
 *
 * The server clamps a page at 200, and the suggestions are a convenience rather
 * than the list of folders that exist -- the field takes any path typed into it
 * -- so one page is the right amount to pay for on a dialog.
 */
const FOLDER_SUGGESTION_LIMIT = 200;

/** What the form holds, as the fields a PATCH would carry. */
interface EditDraft {
  displayName: string;
  description: string;
  tagsInput: string;
  path: string;
}

/**
 * buildUpdate names only what actually changed.
 *
 * Sending a field back unchanged is not free: every metadata field is part of
 * the indexed text, so the server drops the stored vector and re-embeds, and a
 * folder echoed back would be recorded in the audit trail as somebody having
 * refiled the file.
 */
function buildUpdate(r: Resource, draft: EditDraft): { update: ResourceUpdate } | { error: string } {
  const update: ResourceUpdate = {};
  if (draft.displayName.trim() !== r.display_name) update.display_name = draft.displayName.trim();
  if (draft.description.trim() !== r.description) update.description = draft.description.trim();
  const tags = parseTags(draft.tagsInput);
  if (JSON.stringify(tags) !== JSON.stringify(r.tags)) update.tags = tags;
  // A folder change is not metadata: the server rewrites the URI and records
  // the address the file left (#1528).
  if (draft.path !== r.path) {
    const problem = pathProblem(draft.path);
    if (problem) return { error: problem };
    update.path = draft.path;
  }
  return { update };
}

export function EditModal({
  resource: r,
  admin = false,
  onClose,
}: {
  resource: Resource;
  /** True on the administrator's copy of the section, which is what carries the
   * platform-admin override over every library. */
  admin?: boolean;
  onClose: () => void;
}) {
  const update = useUpdateResource();
  // The folders already in this file's own library, offered as completions on
  // the path field. Read here rather than passed in because the dialog opens
  // from the resource's own page, which knows one resource and not a library --
  // and a field that suggested nothing would grow three spellings of the same
  // folder.
  const { data: inThisLibrary } = useResources({
    scope: r.scope,
    scope_id: r.scope_id || undefined,
    limit: FOLDER_SUGGESTION_LIMIT,
  });
  const folders = useMemo(
    () => everyFolder(inThisLibrary?.resources ?? []),
    [inThisLibrary],
  );
  const user = useAuthStore((s) => s.user);
  const { data: personaData } = usePersonas(admin);
  const [displayName, setDisplayName] = useState(r.display_name);
  const [description, setDescription] = useState(r.description);
  const [tagsInput, setTagsInput] = useState(r.tags.join(", "));
  const [path, setPath] = useState(r.path);
  const [error, setError] = useState("");
  const ids = useId();

  // Memoized because handleSave depends on it: rebuilt every render, the
  // callback would be too.
  const here: ScopeTarget = useMemo(
    () => ({ scope: r.scope, scope_id: r.scope_id }),
    [r.scope, r.scope_id],
  );
  const libraries = libraryOptions(
    here,
    user,
    (personaData?.personas ?? []).map((p) => p.name),
    admin ? "admin" : "portal",
  );
  const [library, setLibrary] = useState(targetKey(here));
  const [personEmail, setPersonEmail] = useState("");

  const handleSave = useCallback(async () => {
    const built = buildUpdate(r, { displayName, description, tagsInput, path });
    if ("error" in built) {
      setError(built.error);
      return;
    }
    const upd = built.update;

    // The move is sent only when the library actually changes. Echoing the
    // current one back would be a move to where the file already is, which the
    // server treats as a no-op but which would still read, in the audit trail,
    // as somebody having refiled it.
    if (library !== targetKey(here)) {
      const move = moveFields(
        libraries.find((t) => targetKey(t) === library),
        personEmail,
      );
      if ("error" in move) {
        setError(move.error);
        return;
      }
      Object.assign(upd, move);
    }

    if (Object.keys(upd).length === 0) { onClose(); return; }

    try {
      await update.mutateAsync({ id: r.id, update: upd });
      onClose();
    } catch (err) {
      setError(err instanceof Error ? err.message : "Update failed");
    }
  }, [displayName, description, tagsInput, path, library, libraries, personEmail, here, r, update, onClose]);

  return (
    <ModalShell
      onClose={onClose}
      label="Edit Resource"
      busy={update.isPending}
      bodyClass="space-y-4 p-4"
      header={
        <div className="flex items-center justify-between border-b p-4">
          <h2 className="text-lg font-semibold">Edit Resource</h2>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>
      }
      footer={
        // Save stays reachable without scrolling the form to its end, which is
        // what a short viewport otherwise demands.
        <div className="flex justify-end gap-2 border-t p-4">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSave} disabled={update.isPending}>
            {update.isPending && <Loader2 className="animate-spin" />}
            Save
          </Button>
        </div>
      }
    >
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="space-y-1">
        <Label htmlFor={`${ids}-name`} className="text-xs text-muted-foreground">
          Display Name
        </Label>
        <Input
          id={`${ids}-name`}
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
        />
      </div>

      <div className="space-y-1">
        <Label htmlFor={`${ids}-description`} className="text-xs text-muted-foreground">
          Description
        </Label>
        <Textarea
          id={`${ids}-description`}
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          rows={3}
          className="field-sizing-fixed resize-none"
        />
      </div>

      {libraries.length > 0 && (
        <LibraryField
          id={ids}
          currentKey={targetKey(here)}
          targets={libraries}
          value={library}
          onChange={setLibrary}
          personEmail={personEmail}
          onPersonEmailChange={setPersonEmail}
        />
      )}

      <PathField value={path} onChange={setPath} folders={folders} />

      <div className="space-y-1">
        <Label htmlFor={`${ids}-tags`} className="text-xs text-muted-foreground">
          Tags (comma-separated)
        </Label>
        <Input
          id={`${ids}-tags`}
          value={tagsInput}
          onChange={(e) => setTagsInput(e.target.value)}
        />
      </div>
    </ModalShell>
  );
}
