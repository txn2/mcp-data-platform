import { Plus, Webhook } from "lucide-react";
import { useWebhookSources } from "@/api/admin/hooks";
import type { WebhookSource } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { MODE_LABELS } from "./webhookForm";

// WebhooksPage lists the inbound webhook sources (#1870). A row opens the
// source's page, which is where its address, its status and its rejected
// requests are.

export function WebhooksPage({ onNavigate }: { onNavigate: (path: string) => void }) {
  const { data, isLoading, isError } = useWebhookSources();
  const sources = data?.sources;

  return (
    <div className="space-y-4">
      <div className="flex justify-end">
        <Button type="button" size="sm" onClick={() => onNavigate("/admin/webhooks/new")}>
          <Plus />
          New source
        </Button>
      </div>
      {isError ? (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>
            The webhook sources could not be read. Sources keep receiving whether or not this page can list them.
          </AlertDescription>
        </Alert>
      ) : !isLoading && sources?.length === 0 ? (
        <NoSources />
      ) : (
        <SourcesTable
          sources={sources}
          isLoading={isLoading}
          onOpen={(name) => onNavigate(`/admin/webhooks/${encodeURIComponent(name)}`)}
        />
      )}
    </div>
  );
}

function SourcesTable({
  sources,
  isLoading,
  onOpen,
}: {
  sources: WebhookSource[] | undefined;
  isLoading: boolean;
  onOpen: (name: string) => void;
}) {
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Source</TableHead>
            <TableHead>State</TableHead>
            <TableHead>Authentication</TableHead>
            <TableHead>Table</TableHead>
            <TableHead>Connection</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {isLoading ? (
            <TableRow>
              <TableCell colSpan={5} className="py-6 text-center text-sm text-muted-foreground">
                Loading...
              </TableCell>
            </TableRow>
          ) : (
            (sources ?? []).map((s) => (
              <TableRow key={s.name} className="cursor-pointer" onClick={() => onOpen(s.name)}>
                <TableCell className="font-medium">{s.name}</TableCell>
                <TableCell>
                  {s.enabled ? <Badge variant="info">Enabled</Badge> : <Badge variant="muted">Disabled</Badge>}
                </TableCell>
                <TableCell className="text-sm">{MODE_LABELS[s.auth.mode] ?? s.auth.mode}</TableCell>
                <TableCell className="font-mono text-xs">{s.table}</TableCell>
                <TableCell className="text-sm">{s.connection}</TableCell>
              </TableRow>
            ))
          )}
        </TableBody>
      </Table>
    </div>
  );
}

function NoSources() {
  return (
    <EmptyState icon={Webhook} className="py-10">
      <p className="font-medium text-foreground">No webhook source yet.</p>
      <p className="mx-auto mt-1.5 max-w-lg">
        A source is an address an external system posts events to, such as an email service reporting
        deliveries or a CRM reporting changed contacts. Each event is stored as it arrives, can be queried as a
        table straight away, and is compacted into one Parquet file per window, an hour unless the source sets a shorter one.
      </p>
    </EmptyState>
  );
}
