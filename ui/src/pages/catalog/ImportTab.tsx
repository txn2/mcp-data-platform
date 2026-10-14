import { useState } from "react";
import { Download } from "lucide-react";
import { useCatalogImports, useStartCatalogImport } from "@/api/admin/hooks";
import type { CatalogImport, CatalogImportReport } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { ConfigField, ConfigGroup } from "@/pages/settings/connections/fields";

// ImportTab carries a deployment's DataHub into the platform catalog once
// (#1923): an administrator names the DataHub, starts the import, and reads
// what each run did.

const DEFINITION_LABELS: Record<string, string> = {
  glossary_node: "Glossary nodes",
  glossary_term: "Glossary terms",
  tag: "Tags",
  domain: "Domains",
  data_product: "Data products",
};

export function ImportTab() {
  const { data, isLoading, isError } = useCatalogImports();
  const [selected, setSelected] = useState<string | null>(null);
  const imports = data?.imports ?? [];
  const shown = imports.find((i) => i.id === selected) ?? imports[0];

  return (
    <div className="space-y-4">
      <ImportForm running={imports.some((i) => i.running)} />
      {isError ? (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>The imports could not be read.</AlertDescription>
        </Alert>
      ) : !isLoading && imports.length === 0 ? (
        <EmptyState>Nothing has been imported yet.</EmptyState>
      ) : (
        <>
          <ImportsTable imports={imports} selected={shown?.id} onSelect={setSelected} />
          {shown && <ImportDetail run={shown} />}
        </>
      )}
    </div>
  );
}

// ImportForm names the DataHub and starts an import; it holds while one runs.
function ImportForm({ running }: { running: boolean }) {
  const start = useStartCatalogImport();
  const [url, setUrl] = useState("");
  const [token, setToken] = useState("");
  const ready = !start.isPending && !running && url.trim() !== "" && token.trim() !== "";
  return (
    <ConfigGroup title="Import from DataHub">
      <p className="text-sm text-muted-foreground">
        Copies a DataHub&apos;s datasets, including the records it marked removed, its glossary, tags, domains, data
        products and lineage into the platform catalog, then rewrites the DataHub references that memories, insights,
        knowledge pages and call records hold. Curation lands on the datasets the platform&apos;s scans have
        cataloged, so scan the connections first. Curation already written in the platform is kept, and importing
        again writes only what changed.
      </p>
      <div className="grid max-w-3xl grid-cols-2 gap-3">
        <ConfigField
          label="DataHub URL"
          help="DataHub's GMS address, such as https://datahub.example.com:8080."
          value={url}
          onChange={setUrl}
          mono
          required
        />
        <ConfigField
          label="Access token"
          help="A DataHub personal access token, used for this import and not kept."
          value={token}
          onChange={setToken}
          sensitive
          required
        />
      </div>
      <div className="flex items-center justify-end gap-3">
        {start.isSuccess && <span className="text-sm text-muted-foreground">Import started.</span>}
        <Button type="button" size="sm" disabled={!ready} onClick={() => start.mutate({ url: url.trim(), token })}>
          <Download />
          Import
        </Button>
      </div>
      {start.isError && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>
            {start.error instanceof Error ? start.error.message : "The import could not be started."}
          </AlertDescription>
        </Alert>
      )}
    </ConfigGroup>
  );
}

function ImportsTable({
  imports,
  selected,
  onSelect,
}: {
  imports: CatalogImport[];
  selected?: string;
  onSelect: (id: string) => void;
}) {
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Started</TableHead>
            <TableHead>DataHub</TableHead>
            <TableHead>By</TableHead>
            <TableHead>Status</TableHead>
            <TableHead className="text-right">Datasets</TableHead>
            <TableHead className="text-right">Aspects written</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {imports.map((run) => (
            <TableRow
              key={run.id}
              className="cursor-pointer"
              data-state={run.id === selected ? "selected" : undefined}
              onClick={() => onSelect(run.id)}
            >
              <TableCell>{new Date(run.started_at).toLocaleString()}</TableCell>
              <TableCell className="font-mono text-xs">{run.source_url}</TableCell>
              <TableCell>{run.started_by}</TableCell>
              <TableCell>
                <ImportStatus run={run} />
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {run.report ? run.report.datasets.imported.toLocaleString() : ""}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {run.report ? run.report.aspects_written.toLocaleString() : ""}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function ImportStatus({ run }: { run: CatalogImport }) {
  if (run.running) return <Badge variant="secondary">Running</Badge>;
  if (run.error) return <Badge variant="destructive">Failed</Badge>;
  if (run.finished_at) return <Badge variant="outline">Finished</Badge>;
  return <Badge variant="destructive">Stopped</Badge>;
}

function ImportDetail({ run }: { run: CatalogImport }) {
  return (
    <div className="space-y-4">
      {run.error && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>{run.error}</AlertDescription>
        </Alert>
      )}
      {run.report && <ReportSummary report={run.report} />}
      {run.report && run.report.skipped.length > 0 && <SkippedTable report={run.report} />}
    </div>
  );
}

function ReportSummary({ report }: { report: CatalogImportReport }) {
  const rewritten = Object.values(report.rewrite?.rewritten ?? {}).reduce((a, b) => a + b, 0);
  const rows: [string, number][] = [
    ["Datasets read", report.datasets.read],
    ["Datasets imported", report.datasets.imported],
    ["Datasets filled from removed records", report.datasets.from_removed],
    ["Datasets skipped", report.datasets.skipped],
    ...Object.entries(report.definitions).map(([kind, n]): [string, number] => [DEFINITION_LABELS[kind] ?? kind, n]),
    ["Aspects written", report.aspects_written],
    ["Aspects already current", report.aspects_unchanged],
    ["Aspects kept from the platform", report.aspects_kept],
    ["Lineage edges", report.lineage_edges],
    ["References rewritten", rewritten],
    ["References with no catalog entity", report.rewrite?.unmapped ?? 0],
  ];
  return (
    <ConfigGroup title="Report">
      <dl className="grid max-w-3xl grid-cols-2 gap-x-6 gap-y-1 text-sm">
        {rows.map(([label, n]) => (
          <div key={label} className="flex justify-between border-b py-1">
            <dt className="text-muted-foreground">{label}</dt>
            <dd className="tabular-nums">{n.toLocaleString()}</dd>
          </div>
        ))}
      </dl>
    </ConfigGroup>
  );
}

function SkippedTable({ report }: { report: CatalogImportReport }) {
  const total = Object.values(report.skipped_by_reason).reduce((a, b) => a + b, 0);
  return (
    <ConfigGroup title={`Not carried over (${total.toLocaleString()})`}>
      {total > report.skipped.length && (
        <p className="text-sm text-muted-foreground">The first {report.skipped.length.toLocaleString()} are listed.</p>
      )}
      <div className="rounded-md border">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>DataHub URN</TableHead>
              <TableHead>Reason</TableHead>
              <TableHead>Where</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {report.skipped.map((s, i) => (
              <TableRow key={`${s.urn}-${i}`}>
                <TableCell className="max-w-sm truncate font-mono text-xs" title={s.urn}>
                  {s.urn}
                </TableCell>
                <TableCell className="text-sm">{s.reason}</TableCell>
                <TableCell className="max-w-xs truncate font-mono text-xs" title={s.where}>
                  {s.where}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </div>
    </ConfigGroup>
  );
}
