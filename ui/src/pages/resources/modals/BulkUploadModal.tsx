import { useEffect, useMemo, useRef, useState } from "react";
import { Loader2, X } from "lucide-react";
import { useAuthStore } from "@/stores/auth";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ModalShell } from "@/components/ModalShell";
import { parseTags } from "@/lib/tags";
import { PathField } from "../parts/PathField";
import { pathProblem } from "../parts/pathRules";
import { libraryCopy, targetKey, uploadTargets, withheldUploadPersonas, type ScopeTarget } from "../scopes";
import { DestinationPicker } from "./DestinationPicker";
import { BulkSources } from "./bulk/BulkSources";
import { BulkFileList } from "./bulk/BulkFileList";
import { summarize, summaryText } from "../bulk/progress";
import { useBulkUpload, type Sender } from "../bulk/useBulkUpload";
import type { Unzip } from "../bulk/collect";
import type { SourceFile } from "../bulk/plan";

// The ceiling the dialog assumes when the server has not reported one, which
// is resource.MaxUploadBytes, as in the single-file dialog.
const DEFAULT_MAX_BYTES = 100 * 1024 * 1024;

/**
 * Uploading many files at once: a selection, a folder, or an archive, with the
 * folder, tags and a description template shared across the batch (#1862).
 *
 * Each file is its own request to the upload route, four at a time, so a file
 * the server refuses stops nothing else and every file already stored stays
 * stored. An address that already holds the same bytes is left alone and one
 * holding different bytes gets a new version, which is what makes uploading
 * the same folder again cheap.
 */
export function BulkUploadModal({
  onClose,
  personaNames,
  destination,
  folder,
  folders,
  initial,
  send,
  unzip,
}: {
  onClose: () => void;
  personaNames: string[];
  /** The library in view, or null on the All view, where the dialog asks. */
  destination: ScopeTarget | null;
  /** The folder the person is standing in, the batch's default base folder. */
  folder: string;
  folders: string[];
  /**
   * Files dropped onto the page from the desktop, added to the batch when the
   * dialog opens (#1872). Absent opens it empty.
   */
  initial?: SourceFile[];
  /** Injected by a test; the XMLHttpRequest sender otherwise. */
  send?: Sender;
  unzip?: Unzip;
}) {
  const user = useAuthStore((s) => s.user);
  const maxBytes = user?.max_upload_bytes || DEFAULT_MAX_BYTES;
  const batch = useBulkUpload(maxBytes, send, unzip);
  const choices = useMemo(() => uploadTargets(user, personaNames), [user, personaNames]);
  const [chosen, setChosen] = useState(() => (choices[0] ? targetKey(choices[0]) : ""));
  const [base, setBase] = useState(folder || "samples");
  const [tagsInput, setTagsInput] = useState("");
  const [template, setTemplate] = useState("{name}");
  const [unpack, setUnpack] = useState(true);
  const [error, setError] = useState("");
  const added = useRef(false);
  useEffect(() => {
    if (added.current || !initial?.length) return;
    added.current = true;
    void batch.add(initial, unpack);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [initial]);

  const target: ScopeTarget | null =
    destination ?? choices.find((c) => targetKey(c) === chosen) ?? choices[0] ?? null;
  // A file refused while it was planned counts as not sent, whatever its status.
  const statuses = batch.items.map((i) =>
    i.problem ? { state: "refused" as const, error: i.problem } : batch.statusOf(i.key),
  );
  const summary = summarize(statuses);
  const hasFailed = summary.failed > 0;
  // What Upload would send: every file not stored, not failed, and not refused
  // while planned. One refused at send time is asked again, since the setting
  // that refused it may have changed.
  const remaining = batch.items.filter((item) => {
    const state = batch.statusOf(item.key).state;
    return !item.problem && (state === "ready" || state === "refused");
  }).length;

  const start = async (onlyFailed: boolean) => {
    const problem = !target ? "Choose a folder to upload into." : pathProblem(base);
    if (problem || !target) {
      setError(problem ?? "");
      return;
    }
    setError("");
    await batch.run({ target, base, tags: parseTags(tagsInput), template }, onlyFailed);
  };

  return (
    <ModalShell
      onClose={onClose}
      label="Upload many"
      width="max-w-3xl"
      busy={batch.running}
      bodyClass="space-y-4 p-4"
      header={
        <div className="flex items-center justify-between border-b p-4">
          <h2 className="text-lg font-semibold">Upload many</h2>
          <Button variant="ghost" size="icon-sm" onClick={onClose} aria-label="Close" disabled={batch.running}>
            <X />
          </Button>
        </div>
      }
      footer={
        <BulkFooter
          running={batch.running}
          ran={batch.ran}
          hasFailed={hasFailed}
          count={batch.items.length}
          remaining={remaining}
          onClose={onClose}
          onClear={batch.clear}
          onStop={batch.stop}
          onStart={() => void start(false)}
          onRetry={() => void start(true)}
        />
      }
    >
      {error && (
        <Alert variant="destructive">
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}
      <div className="grid grid-cols-2 gap-3">
        {destination ? (
          <div data-testid="upload-destination" className="rounded-md border bg-muted/40 px-3 py-2">
            <p className="text-xs text-muted-foreground">Destination</p>
            <p className="text-sm font-medium text-foreground">{libraryCopy(destination).name}</p>
            <p className="text-xs text-muted-foreground">{libraryCopy(destination).audience}</p>
          </div>
        ) : (
          <DestinationPicker
            choices={choices}
            value={chosen}
            onChange={setChosen}
            disabled={batch.running}
            withheld={withheldUploadPersonas(user)}
          />
        )}
        <PathField label="Base folder" value={base} onChange={setBase} folders={folders} disabled={batch.running} />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <div className="space-y-1">
          <Label htmlFor="bulk-upload-tags" className="text-xs text-muted-foreground">
            Tags for every file (comma-separated)
          </Label>
          <Input
            id="bulk-upload-tags"
            value={tagsInput}
            onChange={(e) => setTagsInput(e.target.value)}
            placeholder="brand, logos"
            disabled={batch.running}
          />
        </div>
        <div className="space-y-1">
          <Label htmlFor="bulk-upload-description" className="text-xs text-muted-foreground">
            Description ({"{name}"} is each file&apos;s display name)
          </Label>
          <Textarea
            id="bulk-upload-description"
            value={template}
            onChange={(e) => setTemplate(e.target.value)}
            rows={1}
            className="field-sizing-fixed min-h-0 resize-none"
            disabled={batch.running}
          />
        </div>
      </div>
      <BulkSources
        onAdd={(sources) => void batch.add(sources, unpack)}
        unpack={unpack}
        onUnpackChange={setUnpack}
        maxBytes={maxBytes}
        disabled={batch.running}
      />
      {batch.ran && (
        <p className="text-sm text-foreground" data-testid="bulk-summary">
          {summaryText(summary)}
        </p>
      )}
      <BulkFileList
        items={batch.items}
        base={base}
        statusOf={batch.statusOf}
        onRename={batch.rename}
        editable={!batch.running}
      />
    </ModalShell>
  );
}

/** The dialog's actions, which change as the batch goes from picked to run. */
function BulkFooter({
  running,
  ran,
  hasFailed,
  count,
  remaining,
  onClose,
  onClear,
  onStop,
  onStart,
  onRetry,
}: {
  running: boolean;
  ran: boolean;
  hasFailed: boolean;
  count: number;
  /** Files not yet stored and not refused while planned. */
  remaining: number;
  onClose: () => void;
  onClear: () => void;
  onStop: () => void;
  onStart: () => void;
  onRetry: () => void;
}) {
  return (
    <div className="flex items-center justify-between gap-2 border-t p-4">
      <Button variant="ghost" onClick={onClear} disabled={running || count === 0}>
        Clear list
      </Button>
      <div className="flex gap-2">
        {running ? (
          <Button variant="outline" onClick={onStop}>
            Stop after current files
          </Button>
        ) : (
          <Button variant="outline" onClick={onClose}>
            {ran ? "Close" : "Cancel"}
          </Button>
        )}
        {ran && hasFailed && !running && (
          <Button variant="outline" onClick={onRetry}>
            Retry failed
          </Button>
        )}
        <Button onClick={onStart} disabled={running || remaining === 0}>
          {running && <Loader2 className="animate-spin" />}
          {`Upload ${remaining} ${remaining === 1 ? "file" : "files"}`}
        </Button>
      </div>
    </div>
  );
}
