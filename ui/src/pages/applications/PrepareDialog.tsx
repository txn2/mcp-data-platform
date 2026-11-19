import { useState } from "react";
import {
  preparePlanOf,
  usePreparePlan,
  useSavePrepare,
  type ApplicationPrepareFile,
  type ApplicationPreparePlan,
} from "@/api/portal/hooks/applicationEnvironments";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { errorText } from "./applicationFormat";
import { counted, lastEnvName } from "./environmentModel";

// PrepareDialog is the one reviewed change an application written for one
// schema needs before it runs in environments of their own (#2709): every
// script, library and pending migration that names the schema or its roles,
// each shown before and after, the replay of the changed scripts' recorded
// runs, and the save that makes new versions. Saving is an administrator's.

export function PrepareDialog({
  detail,
  isAdmin,
  onClose,
}: {
  detail: ApplicationDetail;
  isAdmin: boolean;
  onClose: () => void;
}) {
  const app = detail.application;
  const { data, isLoading, error } = usePreparePlan(app.id, true);
  const save = useSavePrepare(app.id);
  const [result, setResult] = useState<ApplicationPreparePlan | null>(null);
  const [failure, setFailure] = useState<string | null>(null);
  const plan = result ?? data ?? null;
  const submit = () => {
    setFailure(null);
    save.mutate(undefined, {
      onSuccess: (p) => setResult(p),
      onError: (e) => {
        setFailure(errorText(e, "The change could not be saved"));
        setResult(preparePlanOf(e) ?? result);
      },
    });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-[720px]" data-testid="prepare-dialog">
        <DialogHeader>
          <DialogTitle>Prepare {app.name} for environments</DialogTitle>
          <ChangeLine plan={plan} />
        </DialogHeader>
        {isLoading && <p className="text-sm text-muted-foreground">Reading the application and replaying its runs…</p>}
        <Problem text={error ? errorText(error, "The change could not be read") : failure} />
        {plan && <PrepareBody plan={plan} app={app.name} last={lastEnvName(detail)} />}
        <PrepareFooter plan={plan} isAdmin={isAdmin} busy={save.isPending} onSave={submit} onClose={onClose} />
      </DialogContent>
    </Dialog>
  );
}

/** ChangeLine says how many files change and how. */
function ChangeLine({ plan }: { plan: ApplicationPreparePlan | null }) {
  const n = plan?.files.length ?? 0;
  if (n === 0) return null;
  return (
    <DialogDescription>
      {counted(n, "file")} {n === 1 ? "changes" : "change"}. Each loses the schema and role names it carries; nothing else in it changes.
    </DialogDescription>
  );
}

function Problem({ text }: { text: string | null }) {
  if (!text) return null;
  return (
    <Alert variant="destructive">
      <AlertDescription>{text}</AlertDescription>
    </Alert>
  );
}

/** ValuesList is each schema name inside a quoted SQL value: data, left as written for a person to change. */
function ValuesList({ plan }: { plan: ApplicationPreparePlan }) {
  const values = plan.values ?? [];
  if (values.length === 0) {
    return null;
  }
  return (
    <div className="space-y-1 rounded-lg border p-3" data-testid="prepare-values">
      <p className="text-sm font-medium">Left as written</p>
      <p className="text-xs text-muted-foreground">
        These quoted values hold the schema's name as data, so prepare does not change them. Each environment needs its own text: build it
        from the run's schema, such as current_schema() || '.invoices'.
      </p>
      <ul className="font-mono text-xs">
        {values.map((v) => (
          <li key={`${v.kind}/${v.name}/${v.line}/${v.text}`}>
            {v.name} line {v.line}: '{v.text}'
          </li>
        ))}
      </ul>
    </div>
  );
}

/** PrepareBody is the plan: ready, or each file's change, the replay and what saving does. */
function PrepareBody({ plan, app, last }: { plan: ApplicationPreparePlan; app: string; last: string }) {
  if (plan.files.length === 0) {
    return (
      <>
        <p className="text-sm">Nothing in {app} names its schema or its roles. It is ready for an environment of its own.</p>
        <ValuesList plan={plan} />
      </>
    );
  }
  return (
    <>
      <ValuesList plan={plan} />
      <div className="max-h-[360px] overflow-auto rounded-lg border" data-testid="prepare-files">
        {plan.files.map((f) => (
          <PrepareFileView key={`${f.kind}/${f.id}`} file={f} />
        ))}
      </div>
      <ReplayLine plan={plan} refused={plan.files.filter((f) => f.refusal).length} />
      <p className="text-xs text-muted-foreground">
        Grants come from the platform: each environment's run role gets what the migrations create in its schema. Saving makes new versions;
        release them to {last} like any change, then add the environment.
      </p>
      {plan.saved && (
        <Alert>
          <AlertDescription>Saved. Release the new versions to put them live, then add the environment.</AlertDescription>
        </Alert>
      )}
    </>
  );
}

/** footerState is how many files a save writes, whether it is offered, and whether the gate holds it. */
function footerState(plan: ApplicationPreparePlan | null, isAdmin: boolean): { n: number; saveable: boolean; refused: boolean } {
  if (!plan) return { n: 0, saveable: false, refused: false };
  const n = plan.files.length;
  return { n, saveable: isAdmin && n > 0 && !plan.saved, refused: plan.files.some((f) => f.refusal) };
}

/** PrepareFooter is Cancel, or Done once saved, and an administrator's save. */
function PrepareFooter({
  plan,
  isAdmin,
  busy,
  onSave,
  onClose,
}: {
  plan: ApplicationPreparePlan | null;
  isAdmin: boolean;
  busy: boolean;
  onSave: () => void;
  onClose: () => void;
}) {
  const { n, saveable, refused } = footerState(plan, isAdmin);
  return (
    <DialogFooter>
      <Button variant="ghost" onClick={onClose}>
        {plan?.saved ? "Done" : "Cancel"}
      </Button>
      {saveable && (
        <Button onClick={onSave} disabled={busy || refused}>
          Save {counted(n, "new version")}
        </Button>
      )}
    </DialogFooter>
  );
}

function ReplayLine({ plan, refused }: { plan: ApplicationPreparePlan; refused: number }) {
  if (refused > 0) {
    return (
      <Alert variant="destructive" data-testid="prepare-replay">
        <AlertDescription>
          The save gate refuses {counted(refused, "file")}, so nothing is saved until each is fixed: see the refusal under each.
        </AlertDescription>
      </Alert>
    );
  }
  const scripts = plan.files.filter((f) => f.kind !== "migration");
  const runs = scripts.reduce((n, f) => n + f.replayed.length, 0);
  return (
    <p className="rounded-lg bg-emerald-50 px-3 py-2 text-sm text-emerald-900 dark:bg-emerald-950 dark:text-emerald-100" data-testid="prepare-replay">
      {runs > 0
        ? `Replayed ${counted(runs, "run")} of the changed scripts: every answer is the same.`
        : "The changed scripts have no recorded runs to replay; their tests pass."}
    </p>
  );
}

function fileTitle(f: ApplicationPrepareFile): string {
  if (f.kind === "migration") return `migration ${f.version} (pending)`;
  const saved = f.saved ? f.saved : f.version + 1;
  return `${f.name} · version ${f.version} → ${saved}`;
}

/** PrepareFileView is one file's change: the lines it loses and the lines it gains. */
function PrepareFileView({ file }: { file: ApplicationPrepareFile }) {
  const before = file.before.split("\n");
  const after = file.after.split("\n");
  const kept = new Set(after);
  const had = new Set(before);
  const removed = before.filter((l) => !kept.has(l));
  const added = after.filter((l) => !had.has(l));
  return (
    <section aria-label={fileTitle(file)} className="border-b last:border-b-0" data-testid={`prepare-file-${file.kind}-${file.id}`}>
      <div className="bg-muted px-3 py-2 text-xs font-semibold">{fileTitle(file)}</div>
      <pre className="overflow-x-auto px-3 py-2 font-mono text-xs leading-5 whitespace-pre-wrap">
        {removed.map((l, i) => (
          <span key={`-${i}`} className="block bg-red-100 dark:bg-red-950">
            -{l}
          </span>
        ))}
        {added.map((l, i) => (
          <span key={`+${i}`} className="block bg-emerald-100 dark:bg-emerald-950">
            +{l}
          </span>
        ))}
      </pre>
      {file.refusal && <p className="px-3 pb-2 text-xs text-destructive">{file.refusal}</p>}
    </section>
  );
}
