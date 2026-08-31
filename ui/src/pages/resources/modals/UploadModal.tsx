import { useState, useRef, useCallback, useMemo } from "react";
import { X, Loader2 } from "lucide-react";
import { useUploadResource } from "@/api/resources/hooks";
import { useAuthStore } from "@/stores/auth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { formatBytes } from "@/lib/format";
import { parseTags } from "@/lib/tags";
import { RESOURCE_POSITIONING } from "@/lib/positioning";
import { ModalShell } from "@/components/ModalShell";
import { scopeLabel } from "../shared";
import { PathField } from "../parts/PathField";
import { pathProblem } from "../parts/pathRules";
import { UploadTargets } from "./UploadTargets";
import {
  libraryCopy,
  targetKey,
  uploadTargets,
  type MoveTarget,
  type ScopeTarget,
} from "../scopes";

const MAX_BYTES = 100 * 1024 * 1024;

interface UploadDraft {
  file: File | null;
  displayName: string;
  description: string;
  /** The folder the file is filed into. */
  path: string;
  tagsInput: string;
}

// rejectDraft states why a draft cannot be sent, or null when it can. The
// server validates all of this again; this only spares a round trip per target.
function rejectDraft(draft: UploadDraft): string | null {
  if (!draft.file) return "File is required";
  if (!draft.displayName.trim()) return "Display name is required";
  if (!draft.description.trim()) return "Description is required";
  if (draft.file.size > MAX_BYTES) return "File exceeds 100 MB limit";
  return pathProblem(draft.path);
}

// draftForm builds the multipart body for one target. Every target gets the
// same file and metadata; only the scope differs.
function draftForm(draft: UploadDraft, target: { scope: string; scope_id: string }): FormData {
  const fd = new FormData();
  fd.set("scope", target.scope);
  if (target.scope_id) fd.set("scope_id", target.scope_id);
  fd.set("path", draft.path);
  fd.set("display_name", draft.displayName.trim());
  fd.set("description", draft.description.trim());
  fd.set("file", draft.file!);
  for (const t of parseTags(draft.tagsInput)) fd.append("tags", t);
  return fd;
}

// rejectTargets states why a fan-out scope has nothing to upload to. A global
// upload always has exactly one target, so only the two fan-out scopes can be
// empty here.
function rejectTargets(count: number, scope: string): string | null {
  if (count > 0) return null;
  if (scope === "persona") return "Select at least one persona";
  return "Enter at least one email address";
}

// fanOutMessage reports a multi-target upload. A total failure reads as the
// failure it is; a partial one has to name both halves, because the successes
// are already created and re-running the whole upload would duplicate them.
function fanOutMessage(successes: string[], errors: string[], total: number): string {
  if (errors.length === total) return errors.join("; ");
  return `Succeeded: ${successes.join(", ")}. Failed: ${errors.join("; ")}`;
}

export function UploadModal({
  onClose,
  admin,
  personaNames,
  destination,
  folder,
  folders,
}: {
  onClose: () => void;
  admin: boolean;
  personaNames: string[];
  // The library this upload lands in when the caller is given no choice: the
  // library the picker is showing. The dialog states it before a file is
  // chosen, so the file is never filed somewhere the reader did not expect.
  //
  // Null is the All view, which names no library. An administrator chooses
  // there through the fan-out block below; everyone else gets a plain list of
  // the libraries they may add to, which is what keeps Upload usable on the
  // view every page opens on (#1553).
  destination: ScopeTarget | null;
  // The folder the person is standing in, which is where the file lands unless
  // they change it. Uploading from inside a folder and having the file appear
  // somewhere else is the thing this is for (#1530).
  folder: string;
  // The folders already in this library, offered as completions.
  folders: string[];
}) {
  const upload = useUploadResource();
  const user = useAuthStore((s) => s.user);
  const fileRef = useRef<HTMLInputElement>(null);
  // The admin page's fan-out scope: one upload into every persona chosen, or
  // into every address named. The state below drives that block alone; the
  // ordinary caller's single-destination picker is `chosen`.
  const [scope, setScope] = useState(admin ? "global" : "user");
  // The libraries an ordinary caller may add to, offered when the view names
  // none. Their own library heads the list and is where an upload lands unless
  // they say otherwise.
  const choices = useMemo(() => uploadTargets(user, personaNames), [user, personaNames]);
  const [chosen, setChosen] = useState(() => (choices[0] ? targetKey(choices[0]) : ""));
  const [selectedPersonas, setSelectedPersonas] = useState<string[]>([]);
  const [userEmails, setUserEmails] = useState("");
  const [path, setPath] = useState(folder || "samples");
  const [displayName, setDisplayName] = useState("");
  const [description, setDescription] = useState("");
  const [tagsInput, setTagsInput] = useState("");
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState("");
  const [uploading, setUploading] = useState(false);

  const fixedDestination = libraryCopy(destination);

  function togglePersona(name: string) {
    setSelectedPersonas((prev) =>
      prev.includes(name) ? prev.filter((p) => p !== name) : [...prev, name],
    );
  }

  // Build the list of (scope, scope_id) pairs to upload to.
  const resolveTargets = useCallback((): { scope: string; scope_id: string }[] => {
    // Off the admin page the destination is the library in view, which the
    // caller already chose in the picker and which the page only offers Upload
    // on when they may write to it. A view naming no library falls back to what
    // the dialog's own picker chose.
    if (!admin) {
      if (destination) return [destination];
      const picked = choices.find((c) => targetKey(c) === chosen) ?? choices[0];
      return [picked ?? { scope: "user", scope_id: user?.user_id || "" }];
    }
    if (scope === "global") return [{ scope: "global", scope_id: "" }];
    if (scope === "persona") {
      return selectedPersonas.map((name) => ({ scope: "persona", scope_id: name }));
    }
    const emails = userEmails.split(",").map((e) => e.trim()).filter(Boolean);
    return emails.map((email) => ({ scope: "user", scope_id: email }));
  }, [scope, selectedPersonas, admin, userEmails, user, destination, choices, chosen]);

  const submitting = useRef(false);

  const handleSubmit = useCallback(async () => {
    if (submitting.current) return;
    submitting.current = true;

    const draft: UploadDraft = {
      file,
      displayName,
      description,
      path,
      tagsInput,
    };
    const targets = resolveTargets();
    const rejection = rejectDraft(draft) ?? rejectTargets(targets.length, scope);
    if (rejection) {
      setError(rejection);
      submitting.current = false;
      return;
    }

    setUploading(true);
    setError("");

    const successes: string[] = [];
    const errors: string[] = [];
    for (const target of targets) {
      // Named the way the rest of the Resources surface names a library. The
      // scope id of a user target is a subject identifier, which states
      // nothing to the person reading the result.
      const label = scopeLabel(target.scope, target.scope_id, user);
      try {
        await upload.mutateAsync(draftForm(draft, target));
        successes.push(label);
      } catch (err) {
        errors.push(`${label}: ${err instanceof Error ? err.message : "failed"}`);
      }
    }

    setUploading(false);
    submitting.current = false;
    if (errors.length > 0) {
      setError(fanOutMessage(successes, errors, targets.length));
      return;
    }
    onClose();
  }, [file, displayName, description, scope, path, tagsInput, upload, onClose, resolveTargets, user]);

  return (
    <ModalShell
      onClose={onClose}
      label="Upload Resource"
      busy={uploading}
      bodyClass="space-y-4 p-4"
      header={
        <div className="flex items-center justify-between border-b p-4">
          <h2 className="text-lg font-semibold">Upload Resource</h2>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close">
            <X />
          </Button>
        </div>
      }
      footer={
        // Upload stays reachable however long the target list grows: an admin
        // fanning out to every persona has a scope list bounded only by the
        // deployment's persona count.
        <div className="flex justify-end gap-2 border-t p-4">
          <Button variant="outline" onClick={onClose}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={uploading}>
            {uploading && <Loader2 className="animate-spin" />}
            Upload
          </Button>
        </div>
      }
    >
      <p className="text-xs text-muted-foreground">{RESOURCE_POSITIONING}</p>

      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <div className="space-y-3">
        {admin ? (
          <UploadTargets
            scope={scope}
            onScopeChange={(next) => {
              setScope(next);
              setSelectedPersonas([]);
              setUserEmails("");
            }}
            personaNames={personaNames}
            selectedPersonas={selectedPersonas}
            onTogglePersona={togglePersona}
            userEmails={userEmails}
            onUserEmailsChange={setUserEmails}
          />
        ) : destination ? (
          <div data-testid="upload-destination" className="rounded-md border bg-muted/40 px-3 py-2">
            <p className="text-xs text-muted-foreground">Destination</p>
            <p className="text-sm font-medium text-foreground">{fixedDestination.name}</p>
            <p className="text-xs text-muted-foreground">{fixedDestination.audience}</p>
          </div>
        ) : (
          <DestinationPicker
            choices={choices}
            value={chosen}
            onChange={setChosen}
            disabled={uploading}
          />
        )}
        <PathField
          label="Folder"
          value={path}
          onChange={setPath}
          folders={folders}
          disabled={uploading}
        />
      </div>

      <div className="space-y-1">
        <Label htmlFor="upload-display-name" className="text-xs text-muted-foreground">
          Display Name
        </Label>
        <Input
          id="upload-display-name"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          placeholder="Human-readable name"
        />
      </div>

      <div className="space-y-1">
        <Label htmlFor="upload-description" className="text-xs text-muted-foreground">
          Description
        </Label>
        <Textarea
          id="upload-description"
          value={description}
          onChange={(e) => setDescription(e.target.value)}
          placeholder="What is this and what should the agent do with it?"
          rows={2}
          className="field-sizing-fixed min-h-0 resize-none"
        />
      </div>

      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label htmlFor="upload-tags" className="text-xs text-muted-foreground">
            Tags (comma-separated)
          </Label>
          <Input
            id="upload-tags"
            value={tagsInput}
            onChange={(e) => setTagsInput(e.target.value)}
            placeholder="finance, q4"
          />
        </div>
        <div className="space-y-1">
          <Label className="text-xs text-muted-foreground">File</Label>
          <Button
            type="button"
            variant="outline"
            onClick={() => fileRef.current?.click()}
            className="w-full font-normal"
          >
            <span className="truncate text-xs">
              {file ? `${file.name} (${formatBytes(file.size)})` : "Choose file (max 100 MB)"}
            </span>
          </Button>
          <input ref={fileRef} type="file" className="hidden" onChange={(e) => setFile(e.target.files?.[0] ?? null)} />
        </div>
      </div>
    </ModalShell>
  );
}

/**
 * Which library an upload lands in, for a view that names none.
 *
 * The audience line under it is the point: "Mine" and a persona's library are
 * one click apart, and the difference between them is who else can read the
 * file. A picker that stated only the names would make that difference
 * invisible at the moment it is chosen.
 */
function DestinationPicker({
  choices,
  value,
  onChange,
  disabled,
}: {
  choices: MoveTarget[];
  value: string;
  onChange: (key: string) => void;
  disabled: boolean;
}) {
  const picked = choices.find((c) => targetKey(c) === value);
  return (
    <div className="space-y-1" data-testid="upload-destination-picker">
      <Label className="text-xs text-muted-foreground">Destination</Label>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger aria-label="Destination" className="w-full">
          <SelectValue placeholder="Choose a library" />
        </SelectTrigger>
        <SelectContent>
          {choices.map((c) => (
            <SelectItem key={targetKey(c)} value={targetKey(c)}>
              {c.label}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {picked && (
        <p className="text-xs text-muted-foreground">{libraryCopy(picked).audience}</p>
      )}
    </div>
  );
}
