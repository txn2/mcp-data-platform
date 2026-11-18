import { useState } from "react";
import { ArrowRight } from "lucide-react";
import {
  useAddEnvironment,
  usePromoteEnvironment,
  useRollbackEnvironment,
} from "@/api/portal/hooks/applicationEnvironments";
import type { ApplicationDetail, ApplicationEnvironment } from "@/api/portal/hooks/applications";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { errorText, formatWhen } from "./applicationFormat";
import { Field } from "./NewApplicationDialog";

// ApplicationEnvironments is where an application runs (#2696): a card per
// environment in promotion order, each with the release it runs and where its
// endpoints answer. A release lands in the first; Promote copies a card's
// release to the next; Roll back moves one card back. These controls are the
// person: an environment that requires approval refuses an agent, never
// them. Administrators act; everyone else reads.

interface Props {
  detail: ApplicationDetail;
  isAdmin: boolean;
}

export function ApplicationEnvironments({ detail, isAdmin }: Props) {
  const app = detail.application;
  const envs = app.environments ?? [];
  const [adding, setAdding] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  if (envs.length === 0) return null;
  return (
    <SectionCard
      id="application-environments"
      title="Environments"
      action={
        isAdmin && (
          <Button size="sm" variant="outline" onClick={() => setAdding(true)}>
            Add environment
          </Button>
        )
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          A release lands in {envs[0]!.name} and is promoted one environment at
          a time. Each runs its own release with its own variables;{" "}
          {envs[envs.length - 1]!.name} is what /endpoints serves.
        </p>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <div
          className="flex flex-col gap-3 md:flex-row md:items-stretch"
          data-testid="application-environments"
        >
          {envs.map((e, i) => (
            <div
              key={e.name}
              className="flex flex-1 flex-col gap-3 md:flex-row md:items-stretch"
            >
              {i > 0 && (
                <ArrowRight
                  aria-hidden
                  className="hidden h-5 w-5 shrink-0 self-center text-muted-foreground md:block"
                />
              )}
              <EnvironmentCard
                app={app.id}
                env={e}
                next={envs[i + 1]}
                last={i === envs.length - 1}
                isAdmin={isAdmin}
                onFailure={setFailure}
              />
            </div>
          ))}
        </div>
      </div>
      {adding && (
        <AddEnvironmentDialog
          app={app.id}
          envs={envs}
          onClose={() => setAdding(false)}
        />
      )}
    </SectionCard>
  );
}

function EnvironmentCard({
  app,
  env,
  next,
  last,
  isAdmin,
  onFailure,
}: {
  app: string;
  env: ApplicationEnvironment;
  next?: ApplicationEnvironment;
  last: boolean;
  isAdmin: boolean;
  onFailure: (msg: string | null) => void;
}) {
  return (
    <section
      aria-label={env.name}
      className="flex min-w-0 flex-1 flex-col gap-3 rounded-lg border bg-card p-4"
      data-testid={`environment-${env.name}`}
    >
      <div className="flex items-center justify-between gap-2">
        <h3 className="text-base font-semibold">{env.name}</h3>
        <div className="flex flex-wrap gap-1.5">
          {env.requires_approval && <Badge variant="warning">Requires approval</Badge>}
          {env.release > 0 ? (
            <Badge variant="info" aria-label={`${env.name} runs release ${env.release}`}>
              Release {env.release}
            </Badge>
          ) : (
            <Badge variant="muted">No release</Badge>
          )}
        </div>
      </div>
      <dl className="grid grid-cols-[5rem_1fr] gap-y-1.5 text-sm">
        <dt className="text-muted-foreground">Address</dt>
        <dd className="truncate font-mono text-xs leading-5">
          {last ? "/endpoints/…" : `/environments/${env.name}/endpoints/…`}
        </dd>
      </dl>
      {env.updated_by && (
        <p className="text-xs text-muted-foreground">
          Changed by {env.updated_by}, {formatWhen(env.updated_at)}
        </p>
      )}
      {isAdmin && <EnvironmentActions app={app} env={env} next={next} onFailure={onFailure} />}
    </section>
  );
}

// EnvironmentActions are an administrator's Promote and Roll back on one
// environment.
function EnvironmentActions({
  app,
  env,
  next,
  onFailure,
}: {
  app: string;
  env: ApplicationEnvironment;
  next?: ApplicationEnvironment;
  onFailure: (msg: string | null) => void;
}) {
  const promote = usePromoteEnvironment(app);
  const rollback = useRollbackEnvironment(app);
  const busy = promote.isPending || rollback.isPending;
  const failed = (what: string) => (e: unknown) => onFailure(errorText(e, `${env.name} could not be ${what}`));
  return (
    <div className="mt-auto flex flex-wrap gap-2">
      {next && (
        <Button
          size="sm"
          aria-label={`Promote ${env.name} to ${next.name}`}
          disabled={busy || env.release === 0 || env.release === next.release}
          onClick={() => {
            onFailure(null);
            promote.mutate({ environment: env.name }, { onError: failed("promoted") });
          }}
        >
          Promote to {next.name}
        </Button>
      )}
      <Button
        size="sm"
        variant="outline"
        aria-label={`Roll back ${env.name}`}
        disabled={busy || env.release <= 1}
        onClick={() => {
          onFailure(null);
          rollback.mutate({ environment: env.name }, { onError: failed("rolled back") });
        }}
      >
        Roll back
      </Button>
    </div>
  );
}

function AddEnvironmentDialog({
  app,
  envs,
  onClose,
}: {
  app: string;
  envs: ApplicationEnvironment[];
  onClose: () => void;
}) {
  const add = useAddEnvironment(app);
  const [name, setName] = useState("");
  // The new one goes before the last, which /endpoints serves.
  const choices = envs.slice(0, -1).map((e) => e.name);
  const [after, setAfter] = useState(choices[choices.length - 1] ?? "");
  const [approval, setApproval] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const submit = () => {
    setFailure(null);
    add.mutate(
      { name: name.trim(), after, requires_approval: approval },
      {
        onSuccess: onClose,
        onError: (e) =>
          setFailure(errorText(e, "The environment could not be added")),
      },
    );
  };
  const FIRST = "__first__";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add environment</DialogTitle>
        </DialogHeader>
        <Field id="env-name" label="Name">
          <Input
            id="env-name"
            value={name}
            placeholder="stage"
            onChange={(e) => setName(e.target.value)}
          />
        </Field>
        <Field id="env-after" label="After">
          <Select
            value={after || FIRST}
            onValueChange={(v) => setAfter(v === FIRST ? "" : v)}
          >
            <SelectTrigger id="env-after" aria-label="After" className="w-full">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={FIRST}>First (releases land here)</SelectItem>
              {choices.map((c) => (
                <SelectItem key={c} value={c}>
                  {c}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <label className="flex min-h-11 items-center gap-2 text-sm">
          <input
            type="checkbox"
            checked={approval}
            onChange={(e) => setApproval(e.target.checked)}
          />
          Promotions here need approval
        </label>
        <p className="text-xs text-muted-foreground">
          It runs no release until one is promoted to it.
        </p>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={add.isPending}>
            Cancel
          </Button>
          <Button
            onClick={submit}
            disabled={add.isPending || name.trim() === ""}
          >
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
