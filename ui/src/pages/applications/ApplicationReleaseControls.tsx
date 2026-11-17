import { useState } from "react";
import {
  downloadApplicationExport,
  useReleaseApplication,
  useRollbackApplication,
  type ApplicationDetail,
} from "@/api/portal/hooks/applications";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { ACTION_LABEL, errorText, formatWhen } from "./applicationFormat";
import { Field } from "./NewApplicationDialog";

// ReleaseControls are an administrator's actions on an application (#2411):
// release (pin every part's newest version and apply pending migrations), roll
// back to an earlier release's pins, and export a release as a bundle. The
// server refuses each for anyone else; the page shows them to nobody else.

type Open = "release" | "rollback" | null;

export function ReleaseControls({ detail }: { detail: ApplicationDetail }) {
  const app = detail.application;
  const [open, setOpen] = useState<Open>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const earlier = detail.releases.filter((r) => r.number < app.current_release);
  const released = app.current_release > 0;

  const exportBundle = () => {
    setFailure(null);
    downloadApplicationExport(app.id, app.current_release).catch((e: unknown) =>
      setFailure(errorText(e, "The release could not be exported")),
    );
  };

  return (
    <div className="flex flex-col items-end gap-2">
      <div className="flex flex-wrap gap-2">
        <Button size="sm" variant="outline" disabled={!released} onClick={exportBundle}>
          Export
        </Button>
        <Button
          size="sm"
          variant="outline"
          disabled={earlier.length === 0}
          onClick={() => setOpen("rollback")}
        >
          Roll back
        </Button>
        <Button size="sm" disabled={blockingProblems(detail).length > 0} onClick={() => setOpen("release")}>
          Release
        </Button>
      </div>
      {failure && (
        <Alert variant="destructive">
          <AlertDescription>{failure}</AlertDescription>
        </Alert>
      )}
      {open === "release" && <ReleaseDialog app={app.id} detail={detail} onClose={() => setOpen(null)} />}
      {open === "rollback" && (
        <RollbackDialog app={app.id} detail={detail} onClose={() => setOpen(null)} />
      )}
    </div>
  );
}

/**
 * blockingProblems is what refuses a release: every problem except the
 * message of a diagram finding that does not block (#2540), which is advice.
 */
export function blockingProblems(detail: ApplicationDetail): string[] {
  const advice = new Set((detail.findings ?? []).filter((f) => !f.blocks).map((f) => f.message));
  return detail.problems.filter((p) => !advice.has(p));
}

function ReleaseDialog({ app, detail, onClose }: { app: string; detail: ApplicationDetail; onClose: () => void }) {
  const release = useReleaseApplication(app);
  const [note, setNote] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const [moving, setMoving] = useState<string[]>([]);
  const [force, setForce] = useState(false);
  const parts = detail.site_parts ?? [];
  // A refusal over the contract can be overridden by the administrator
  // releasing, which the release records (#2679).
  const contractRefusal = failure !== null && failure.includes("contract");
  const submit = () => {
    setFailure(null);
    release.mutate(
      { note: note.trim() || undefined, move_sites: moving, force: contractRefusal && force },
      {
        onSuccess: onClose,
        onError: (e) => setFailure(errorText(e, "The application could not be released")),
      },
    );
  };
  const toggle = (id: string) => setMoving((m) => (m.includes(id) ? m.filter((x) => x !== id) : [...m, id]));
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Release</DialogTitle>
        </DialogHeader>
        {parts.length > 0 && (
          <div className="space-y-2 text-sm" data-testid="release-site-parts">
            <p className="font-medium">Site environments this release moves</p>
            {parts.map((p) =>
              p.release_mode === "coupled" ? (
                <div key={p.part_id} className="flex justify-between rounded-md border px-3 py-2">
                  <span>
                    {p.part_id} <span className="text-xs text-amber-700 dark:text-amber-300">coupled</span>
                  </span>
                  <span className="text-muted-foreground">{p.step && p.step !== p.serves ? `${p.serves} → ${p.step}` : `stays at ${p.serves}`}</span>
                </div>
              ) : (
                <label key={p.part_id} className="flex min-h-11 items-center gap-2 rounded-md border px-3 py-2">
                  <input type="checkbox" checked={moving.includes(p.part_id)} onChange={() => toggle(p.part_id)} />
                  <span className="flex-1">
                    {p.part_id} <span className="text-xs text-blue-700 dark:text-blue-300">linked</span>
                  </span>
                  <span className="text-muted-foreground">
                    {moving.includes(p.part_id) && p.step ? `${p.serves} → ${p.step}` : `stays at ${p.serves}`}
                  </span>
                </label>
              ),
            )}
            <p className="text-xs text-muted-foreground">
              Tick a linked part to move it one step in this release too. A rollback of this release returns only the parts it moved.
            </p>
          </div>
        )}
        <Field id="release-note" label="Note">
          <Textarea id="release-note" rows={3} value={note} onChange={(e) => setNote(e.target.value)} />
        </Field>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        {contractRefusal && (
          <label className="flex min-h-11 items-center gap-2 text-sm">
            <input type="checkbox" checked={force} onChange={(e) => setForce(e.target.checked)} />
            Release anyway (administrator, recorded with the release)
          </label>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={release.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={release.isPending || (contractRefusal && !force)}>
            Release
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RollbackDialog({
  app,
  detail,
  onClose,
}: {
  app: string;
  detail: ApplicationDetail;
  onClose: () => void;
}) {
  const rollback = useRollbackApplication(app);
  // Releases are newest first, so the first one older than the current is the
  // previous release, which is what a roll back restores unless told otherwise.
  const earlier = detail.releases.filter((r) => r.number < detail.application.current_release);
  const [target, setTarget] = useState(String(earlier[0]?.number ?? ""));
  const [failure, setFailure] = useState<string | null>(null);
  const submit = () => {
    setFailure(null);
    rollback.mutate(
      { to_release: Number(target) },
      {
        onSuccess: onClose,
        onError: (e) => setFailure(errorText(e, "The application could not be rolled back")),
      },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Roll back</DialogTitle>
        </DialogHeader>
        <Field id="rollback-release" label="Release">
          <Select value={target} onValueChange={setTarget}>
            <SelectTrigger id="rollback-release" aria-label="Release" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {earlier.map((r) => (
                <SelectItem key={r.number} value={String(r.number)}>
                  {r.number} · {ACTION_LABEL[r.action] ?? r.action} · {formatWhen(r.created_at)}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={rollback.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={target === "" || rollback.isPending}>
            Roll back
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
