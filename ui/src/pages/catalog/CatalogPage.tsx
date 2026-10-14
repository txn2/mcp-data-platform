import { useEffect, useState } from "react";
import { Plus, RefreshCw, Trash2 } from "lucide-react";
import {
  useCatalogScans,
  useCatalogSettings,
  useConnectionInstances,
  useRequestCatalogScan,
  useSaveCatalogSettings,
} from "@/api/admin/hooks";
import type { CatalogScan } from "@/api/admin/types";
import { EmptyState } from "@/components/patterns/EmptyState";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
} from "@/pages/settings/connections/fields";
import {
  type CatalogForm,
  connectionOptions,
  formFromSettings,
  settingsFromForm,
} from "./catalogForm";
import { DocumentsTab } from "./DocumentsTab";
import { ImportTab } from "./ImportTab";

// CatalogPage is where an administrator sees each connection's last scan into
// the platform catalog and sets what the scan does (#1918): how often it runs,
// and the catalogs, schemas or operations each connection leaves out. The
// Documents tab lists the schema documents the scan keeps, and the Import tab
// carries a DataHub into the catalog (#1923, #2124).

type Tab = "scans" | "settings" | "documents" | "import";

const TABS: { key: Tab; label: string }[] = [
  { key: "scans", label: "Scans" },
  { key: "settings", label: "Settings" },
  { key: "documents", label: "Documents" },
  { key: "import", label: "Import" },
];

export function CatalogPage({ initialTab }: { initialTab?: string }) {
  const [tab, setTab] = useState<Tab>(
    TABS.some((t) => t.key === initialTab) ? (initialTab as Tab) : "scans",
  );
  return (
    <Tabs value={tab} onValueChange={(v) => setTab(v as Tab)} className="gap-4">
      <TabsList
        variant="line"
        className="group-data-[orientation=horizontal]/tabs:h-auto w-full justify-start gap-1 border-b p-0"
      >
        {TABS.map((t) => (
          <TabsTrigger
            key={t.key}
            value={t.key}
            className="flex-none px-4 py-2 group-data-[orientation=horizontal]/tabs:after:bottom-[-1px]"
          >
            {t.label}
          </TabsTrigger>
        ))}
      </TabsList>
      <TabsContent value="scans">
        <ScansTab />
      </TabsContent>
      <TabsContent value="settings">
        <SettingsTab />
      </TabsContent>
      <TabsContent value="documents">
        <DocumentsTab />
      </TabsContent>
      <TabsContent value="import">
        <ImportTab />
      </TabsContent>
    </Tabs>
  );
}

function ScansTab() {
  const { data, isLoading, isError } = useCatalogScans();
  const request = useRequestCatalogScan();
  const scans = data?.scans;
  return (
    <div className="space-y-4">
      <div className="flex items-center justify-end gap-3">
        {request.isSuccess && (
          <span className="text-sm text-muted-foreground">
            Scan requested. This list updates when it finishes.
          </span>
        )}
        <Button
          type="button"
          size="sm"
          disabled={request.isPending}
          onClick={() => request.mutate()}
        >
          <RefreshCw />
          Scan now
        </Button>
      </div>
      {request.isError && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>The scan could not be requested.</AlertDescription>
        </Alert>
      )}
      {isError ? (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>The scans could not be read.</AlertDescription>
        </Alert>
      ) : !isLoading && scans?.length === 0 ? (
        <EmptyState>No connection has been scanned yet.</EmptyState>
      ) : (
        <ScansTable scans={scans ?? []} />
      )}
    </div>
  );
}

function ScansTable({ scans }: { scans: CatalogScan[] }) {
  return (
    <div className="rounded-md border">
      <Table>
        <TableHeader>
          <TableRow>
            <TableHead>Connection</TableHead>
            <TableHead>Kind</TableHead>
            <TableHead>Last scan</TableHead>
            <TableHead className="text-right">Datasets</TableHead>
            <TableHead className="text-right">Removed</TableHead>
            <TableHead>Problem</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          {scans.map((s) => (
            <TableRow key={`${s.connection_kind}/${s.connection}`}>
              <TableCell className="font-medium">{s.connection}</TableCell>
              <TableCell>{s.connection_kind}</TableCell>
              <TableCell>{new Date(s.finished_at).toLocaleString()}</TableCell>
              <TableCell className="text-right tabular-nums">
                {s.datasets.toLocaleString()}
              </TableCell>
              <TableCell className="text-right tabular-nums">
                {s.removed.toLocaleString()}
              </TableCell>
              <TableCell className="max-w-md text-destructive">
                {s.error && (
                  <div className="truncate" title={s.error}>
                    {s.error}
                  </div>
                )}
                {(s.unread ?? []).map((u) => (
                  <div key={u.path} className="truncate" title={u.error}>
                    Not read: {u.path}
                  </div>
                ))}
              </TableCell>
            </TableRow>
          ))}
        </TableBody>
      </Table>
    </div>
  );
}

function SettingsTab() {
  const settings = useCatalogSettings();
  const scans = useCatalogScans();
  const instances = useConnectionInstances();
  const save = useSaveCatalogSettings();
  const [form, setForm] = useState<CatalogForm | null>(null);

  useEffect(() => {
    if (settings.data && form === null) {
      setForm(
        formFromSettings(settings.data.scan_interval, settings.data.exclusions),
      );
    }
  }, [settings.data, form]);

  if (settings.isError) {
    return (
      <Alert variant="destructive" className="py-2">
        <AlertDescription>
          The catalog settings could not be read.
        </AlertDescription>
      </Alert>
    );
  }
  if (form === null) return null;

  const options = connectionOptions(scans.data?.scans, instances.data);
  const setRow = (i: number, patch: Partial<CatalogForm["rows"][number]>) =>
    setForm({
      ...form,
      rows: form.rows.map((r, j) => (j === i ? { ...r, ...patch } : r)),
    });

  return (
    <div className="max-w-3xl space-y-4">
      <ConfigGroup title="Schedule">
        <ConfigField
          label="Scan every"
          help="A duration such as 6h or 90m, at least 5m."
          value={form.scanEvery}
          onChange={(v) => setForm({ ...form, scanEvery: v })}
          mono
        />
      </ConfigGroup>
      <ConfigGroup
        title="Exclusions"
        action={
          <Button
            type="button"
            size="sm"
            variant="outline"
            onClick={() =>
              setForm({
                ...form,
                rows: [...form.rows, { connection: "", leaveOut: "" }],
              })
            }
          >
            <Plus />
            Add exclusion
          </Button>
        }
      >
        {form.rows.length === 0 && (
          <p className="text-sm text-muted-foreground">
            Every connection is scanned in full.
          </p>
        )}
        {form.rows.map((row, i) => (
          <div
            key={i}
            className="grid grid-cols-[1fr_2fr_auto] items-start gap-3"
          >
            <ConfigSelect
              label="Connection"
              value={row.connection}
              options={options}
              onChange={(v) => setRow(i, { connection: v })}
            />
            <ConfigField
              label="Leave out"
              help="Catalogs, catalog.schema, or operation prefixes, separated by commas."
              value={row.leaveOut}
              onChange={(v) => setRow(i, { leaveOut: v })}
              mono
            />
            <Button
              type="button"
              size="icon"
              variant="ghost"
              className="mt-6"
              aria-label="Remove"
              onClick={() =>
                setForm({ ...form, rows: form.rows.filter((_, j) => j !== i) })
              }
            >
              <Trash2 />
            </Button>
          </div>
        ))}
      </ConfigGroup>
      {save.isError && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>
            {save.error instanceof Error
              ? save.error.message
              : "The settings could not be saved."}
          </AlertDescription>
        </Alert>
      )}
      <div className="flex items-center justify-end gap-3">
        {save.isSuccess && (
          <span className="text-sm text-muted-foreground">
            Saved. The next scan uses these settings.
          </span>
        )}
        <Button
          type="button"
          size="sm"
          disabled={save.isPending}
          onClick={() => save.mutate(settingsFromForm(form))}
        >
          Save
        </Button>
      </div>
    </div>
  );
}
