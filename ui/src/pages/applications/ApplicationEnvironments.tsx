import { useState } from "react";
import { ArrowRight } from "lucide-react";
import { useRollbackEnvironment } from "@/api/portal/hooks/applicationEnvironments";
import type { ApplicationDetail, ApplicationEnvironment } from "@/api/portal/hooks/applications";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { errorText, formatWhen } from "./applicationFormat";
import { AddEnvironmentDialog, PromoteDialog, RemoveEnvironmentDialog } from "./EnvironmentDialogs";
import { counted, envSchemaOf, prepareTotal, siteLabel, sitePartsCalling } from "./environmentModel";
import { PrepareDialog } from "./PrepareDialog";

// ApplicationEnvironments is where an application runs (#2696): a card per
// environment in promotion order, each with the release it runs, where its
// endpoints answer, the schema it runs in and the site environments that call
// it (#2709). A release lands in the first; Promote copies a card's release
// to the next, after a dialog listing what that does; Roll back moves one
// card back. These controls are the person: an environment that requires
// approval refuses an agent, never them. Administrators act; everyone else
// reads. An application whose files still name its schema or roles is shown
// the prepare banner, and adds no environment until prepare saves their
// change.

interface Props {
  detail: ApplicationDetail;
  isAdmin: boolean;
}

export function ApplicationEnvironments({ detail, isAdmin }: Props) {
  const app = detail.application;
  const envs = app.environments ?? [];
  const [adding, setAdding] = useState(false);
  const [preparing, setPreparing] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  if (envs.length === 0) return null;
  const unprepared = prepareTotal(detail.prepare) > 0;
  return (
    <SectionCard
      id="application-environments"
      title="Environments"
      action={
        isAdmin && (
          <Button
            size="sm"
            variant="outline"
            disabled={unprepared}
            title={unprepared ? "Prepare the application first" : undefined}
            onClick={() => setAdding(true)}
          >
            Add environment
          </Button>
        )
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          A release lands in {envs[0]!.name} and is promoted one environment at a time. Each runs its own release with its own variables
          {app.schema ? " and its own schema" : ""}; {envs[envs.length - 1]!.name} is what /endpoints serves. A site environment calls the
          environment with its name.
        </p>
        {unprepared && <PrepareBanner detail={detail} onReview={() => setPreparing(true)} />}
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <div className="flex flex-col gap-3 md:flex-row md:items-stretch" data-testid="application-environments">
          {envs.map((e, i) => (
            <div key={e.name} className="flex flex-1 flex-col gap-3 md:flex-row md:items-stretch">
              {i > 0 && <ArrowRight aria-hidden className="hidden h-5 w-5 shrink-0 self-center text-muted-foreground md:block" />}
              <EnvironmentCard
                detail={detail}
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
      {adding && <AddEnvironmentDialog detail={detail} onClose={() => setAdding(false)} />}
      {preparing && <PrepareDialog detail={detail} isAdmin={isAdmin} onClose={() => setPreparing(false)} />}
    </SectionCard>
  );
}

/** PrepareBanner says what still names the schema, and opens the change. */
function PrepareBanner({ detail, onReview }: { detail: ApplicationDetail; onReview: () => void }) {
  const app = detail.application;
  const envs = app.environments ?? [];
  const c = detail.prepare!;
  return (
    <div
      className="flex flex-col gap-2 rounded-lg border border-blue-200 bg-blue-50 p-3 text-sm text-blue-950 dark:border-blue-900 dark:bg-blue-950 dark:text-blue-50"
      data-testid="prepare-banner"
    >
      <span>
        {envs.length === 1 && (
          <b>
            {app.name} runs in {envs[0]!.name} only.{" "}
          </b>
        )}
        {counted(c.scripts, "script")}, {counted(c.libraries, "library", "libraries")} and {counted(c.migrations, "pending migration")} name
        the schema {app.schema} or its roles. A second environment would run them against {envs[envs.length - 1]!.name}'s schema, so prepare
        them first.
      </span>
      <Button size="sm" className="self-start" onClick={onReview}>
        Review the change
      </Button>
    </div>
  );
}

function EnvironmentCard({
  detail,
  env,
  next,
  last,
  isAdmin,
  onFailure,
}: {
  detail: ApplicationDetail;
  env: ApplicationEnvironment;
  next?: ApplicationEnvironment;
  last: boolean;
  isAdmin: boolean;
  onFailure: (msg: string | null) => void;
}) {
  const schema = envSchemaOf(detail, env.name);
  const sites = sitePartsCalling(detail, env.name);
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
        <dd className="truncate font-mono text-xs leading-5">{last ? "/endpoints/…" : `/environments/${env.name}/endpoints/…`}</dd>
        {schema && (
          <>
            <dt className="text-muted-foreground">Schema</dt>
            <dd className="flex min-w-0 flex-wrap items-baseline gap-x-2" data-testid={`environment-schema-${env.name}`}>
              <span className="font-mono text-xs">{schema.schema}</span>
              <span className="text-xs text-muted-foreground">
                on {detail.application.connection} · migration {schema.migration_version}
                {schema.shared ? ` · shared with ${schema.shared}` : ""}
              </span>
              {schema.problem && <span className="text-xs text-destructive">{schema.problem}</span>}
            </dd>
          </>
        )}
        <dt className="text-muted-foreground">Sites</dt>
        <dd className="flex min-w-0 flex-col gap-0.5" data-testid={`environment-sites-${env.name}`}>
          {sites.length === 0 ? (
            <span className="text-muted-foreground">None</span>
          ) : (
            sites.map((p) => <span key={p.part_id}>{siteLabel(p.part_id)}</span>)
          )}
        </dd>
      </dl>
      {env.updated_by && (
        <p className="text-xs text-muted-foreground">
          Changed by {env.updated_by}, {formatWhen(env.updated_at)}
        </p>
      )}
      {isAdmin && <EnvironmentActions detail={detail} env={env} next={next} last={last} onFailure={onFailure} />}
    </section>
  );
}

// EnvironmentActions are an administrator's Promote, Roll back and Remove on
// one environment.
function EnvironmentActions({
  detail,
  env,
  next,
  last,
  onFailure,
}: {
  detail: ApplicationDetail;
  env: ApplicationEnvironment;
  next?: ApplicationEnvironment;
  last: boolean;
  onFailure: (msg: string | null) => void;
}) {
  const rollback = useRollbackEnvironment(detail.application.id);
  const [promoting, setPromoting] = useState(false);
  const [removing, setRemoving] = useState(false);
  return (
    <div className="mt-auto flex flex-wrap gap-2">
      {next && (
        <Button
          size="sm"
          aria-label={`Promote ${env.name} to ${next.name}`}
          disabled={rollback.isPending || env.release === 0 || env.release === next.release}
          onClick={() => {
            onFailure(null);
            setPromoting(true);
          }}
        >
          Promote to {next.name}
        </Button>
      )}
      <Button
        size="sm"
        variant="outline"
        aria-label={`Roll back ${env.name}`}
        disabled={rollback.isPending || env.release <= 1}
        onClick={() => {
          onFailure(null);
          rollback.mutate(
            { environment: env.name },
            { onError: (e: unknown) => onFailure(errorText(e, `${env.name} could not be rolled back`)) },
          );
        }}
      >
        Roll back
      </Button>
      {!last && (
        <Button size="sm" variant="ghost" aria-label={`Remove ${env.name}`} onClick={() => setRemoving(true)}>
          Remove
        </Button>
      )}
      {promoting && next && <PromoteDialog detail={detail} from={env.name} to={next.name} onClose={() => setPromoting(false)} />}
      {removing && <RemoveEnvironmentDialog detail={detail} env={env.name} onClose={() => setRemoving(false)} />}
    </div>
  );
}
