import { Boxes } from "lucide-react";
import { useApplication, type Application } from "@/api/portal/hooks/applications";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { useAuthStore } from "@/stores/auth";
import { ApplicationParts } from "./ApplicationParts";
import { ApplicationReleases } from "./ApplicationReleases";

// ApplicationDetailPage is one application (#2411): who owns it, the release
// in force, its parts with the versions that release pins, and its releases.
// Everyone signed in reads it; the owner and administrators change its parts;
// releasing, rolling back and exporting are administrators' actions.

interface Props {
  app: string;
  onNavigate: (path: string) => void;
  onBack: () => void;
}

export function ApplicationDetailPage({ app, onNavigate, onBack }: Props) {
  const { data, isLoading, error } = useApplication(app);
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const email = useAuthStore((s) => s.user?.email ?? "");

  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading…</p>;
  }
  if (error || !data) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel="Applications" onBack={onBack} icon={Boxes} title="Application" />
        <Alert variant="destructive">
          <AlertDescription>{error?.message ?? "This application could not be loaded."}</AlertDescription>
        </Alert>
      </div>
    );
  }

  const a = data.application;
  const canEdit = isAdmin || isOwner(a, email);
  return (
    <div className="space-y-4">
      <PageHeader
        backLabel="Applications"
        onBack={onBack}
        icon={Boxes}
        title={a.name}
        subtitle={a.description}
        actions={
          a.current_release > 0 ? (
            <Badge variant="info">Release {a.current_release}</Badge>
          ) : (
            <Badge variant="muted">Not released</Badge>
          )
        }
      />
      <ApplicationFacts application={a} migrations={data.migrations} />
      <ApplicationParts detail={data} canEdit={canEdit} isAdmin={isAdmin} onNavigate={onNavigate} />
      <ApplicationReleases detail={data} isAdmin={isAdmin} />
    </div>
  );
}

function isOwner(a: Application, email: string): boolean {
  return email !== "" && a.owner_email.toLowerCase() === email.toLowerCase();
}

function ApplicationFacts({
  application: a,
  migrations,
}: {
  application: Application;
  migrations?: { applied: number; pending: number[] };
}) {
  return (
    <dl className="grid gap-x-6 gap-y-2 text-sm sm:grid-cols-3" data-testid="application-facts">
      <Fact label="Owner" value={a.owner_email} />
      {a.connection && <Fact label="Schema" value={`${a.connection} / ${a.schema}`} />}
      {migrations && <Fact label="Migrations" value={String(migrations.applied)} />}
    </dl>
  );
}

function Fact({ label, value }: { label: string; value: string }) {
  return (
    <div className="min-w-0">
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className="truncate">{value}</dd>
    </div>
  );
}
