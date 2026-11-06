import { useState } from "react";
import { Boxes } from "lucide-react";
import { useApplications, type Application } from "@/api/portal/hooks/applications";
import { EmptyState } from "@/components/patterns/EmptyState";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useAuthStore } from "@/stores/auth";
import { ImportApplicationDialog } from "./ImportApplicationDialog";
import { NewApplicationDialog } from "./NewApplicationDialog";
import { formatWhen } from "./applicationFormat";

// ApplicationsPage lists every application (#2411). Everyone signed in reads
// it and may record a new one; importing a bundle is an administrator's action,
// so its button appears for nobody else.

interface Props {
  onNavigate: (path: string) => void;
}

export function ApplicationsPage({ onNavigate }: Props) {
  const { data, isLoading, error } = useApplications();
  const isAdmin = useAuthStore((s) => s.isAdmin());
  const [creating, setCreating] = useState(false);
  const [importing, setImporting] = useState(false);
  const open = (app: string) => onNavigate(`/applications/${encodeURIComponent(app)}`);

  const actions = (
    <div className="flex flex-wrap gap-2">
      {isAdmin && (
        <Button size="sm" variant="outline" onClick={() => setImporting(true)}>
          Import
        </Button>
      )}
      <Button size="sm" onClick={() => setCreating(true)}>
        New application
      </Button>
    </div>
  );

  return (
    <div className="space-y-4">
      <SectionCard title="Applications" action={actions}>
        {error ? (
          <Alert variant="destructive">
            <AlertDescription>{error.message}</AlertDescription>
          </Alert>
        ) : isLoading ? (
          <p className="text-sm text-muted-foreground">Loading…</p>
        ) : (
          <ApplicationTable applications={data?.applications ?? []} onOpen={open} />
        )}
      </SectionCard>
      {creating && (
        <NewApplicationDialog
          onNavigate={onNavigate}
          onClose={() => setCreating(false)}
          onCreated={(a) => {
            setCreating(false);
            open(a.id);
          }}
        />
      )}
      {importing && (
        <ImportApplicationDialog
          onClose={() => setImporting(false)}
          onImported={(name) => {
            setImporting(false);
            open(name);
          }}
        />
      )}
    </div>
  );
}

function ApplicationTable({
  applications,
  onOpen,
}: {
  applications: Application[];
  onOpen: (app: string) => void;
}) {
  if (applications.length === 0) {
    return <EmptyState icon={Boxes}>No applications</EmptyState>;
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Owner</TableHead>
          <TableHead>Parts</TableHead>
          <TableHead>Current release</TableHead>
          <TableHead>Updated</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {applications.map((a) => (
          <TableRow
            key={a.id}
            className="cursor-pointer align-top"
            onClick={() => onOpen(a.id)}
            data-testid={`application-row-${a.id}`}
          >
            <TableCell className="whitespace-normal">
              <div className="font-medium">{a.name}</div>
              {a.description && (
                <div className="text-xs text-muted-foreground">{a.description}</div>
              )}
            </TableCell>
            <TableCell className="text-sm">{a.owner_email}</TableCell>
            <TableCell className="text-sm">{a.parts.length}</TableCell>
            <TableCell>
              {a.current_release > 0 ? (
                <Badge variant="info">{a.current_release}</Badge>
              ) : (
                <Badge variant="muted">None</Badge>
              )}
            </TableCell>
            <TableCell className="text-sm text-muted-foreground">
              {formatWhen(a.updated_at)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
