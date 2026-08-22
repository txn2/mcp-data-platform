import { useState } from "react";
import { Table2, Trash2, Loader2, AlertTriangle, Plus } from "lucide-react";
import {
  useTableRegistrations,
  useTableConnections,
  useRegisterTable,
  useUnregisterTable,
  TableApiError,
} from "@/api/tables/hooks";
import type { TableRegistration, TableSourceKind } from "@/api/tables/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { EmptyState } from "@/components/patterns/EmptyState";
import { CopyButton } from "@/components/provenance/parts";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

// TablesPanel is the register / unregister surface for a stored file, shared
// by the managed-resource detail modal and the asset viewer (#1327). One panel
// serves both because the action is the same on either: point a query engine
// at the file where it already sits, without copying it anywhere.
//
// It renders nothing at all when the deployment cannot register -- no Trino
// connection carries a scratch catalog and schema -- rather than showing a
// control that always refuses.
export function TablesPanel({
  kind,
  id,
  contentType,
  filename,
  canModify,
}: {
  kind: TableSourceKind;
  id: string;
  /** contentType decides whether the panel appears: only a CSV can be a table. */
  contentType: string;
  /** filename seeds the suggested table name. */
  filename?: string;
  /** canModify gates the register and unregister controls, not the listing. */
  canModify: boolean;
}) {
  const { visible, connections, registrations, isLoading } = usePanelData(kind, id, contentType);
  const [adding, setAdding] = useState(false);

  if (!visible) {
    return null;
  }
  const canOffer = canModify && connections.length > 0;

  return (
    <SectionCard
      title="Query as a table"
      action={<RegisterAction shown={canOffer && !adding} onClick={() => setAdding(true)} />}
    >
      <p className="text-xs text-muted-foreground">
        Registering points a query engine at this file where it already sits. Nothing is copied, so
        the table always reads the file&rsquo;s current contents. Every column comes back as text.
      </p>

      {adding && (
        <RegisterForm
          kind={kind}
          id={id}
          filename={filename}
          connections={connections}
          onDone={() => setAdding(false)}
        />
      )}

      <RegistrationList
        kind={kind}
        id={id}
        registrations={registrations}
        isLoading={isLoading}
        canModify={canModify}
      />
    </SectionCard>
  );
}

// usePanelData reads what the panel needs and decides whether it appears at
// all.
//
// A file that is not a CSV cannot be a table, and a deployment where no
// connection carries a scratch catalog and schema has nowhere to put one. In
// both cases the panel is absent rather than empty: an explanation of an
// action that was never available is noise on a page about the file. A file
// that IS registered stays visible either way, because a table someone can
// still query must not vanish from the page just because the connection it
// lives on stopped offering new ones.
function usePanelData(kind: TableSourceKind, id: string, contentType: string) {
  const isCSV = contentType.toLowerCase().includes("csv");
  const connectionQuery = useTableConnections(isCSV);
  const registrationQuery = useTableRegistrations(kind, isCSV ? id : undefined);

  const connections = connectionQuery.data?.connections ?? [];
  const registrations = registrationQuery.data?.registrations ?? [];
  return {
    visible: isCSV && (connections.length > 0 || registrations.length > 0),
    connections,
    registrations,
    isLoading: registrationQuery.isLoading,
  };
}

// RegisterAction is the section's own control, rendered only while there is
// somewhere to register onto and no form already open.
function RegisterAction({ shown, onClick }: { shown: boolean; onClick: () => void }) {
  if (!shown) {
    return null;
  }
  return (
    <Button type="button" variant="outline" size="xs" onClick={onClick}>
      <Plus />
      Register
    </Button>
  );
}

// RegistrationList renders the tables over a file, or says there are none.
function RegistrationList({
  kind,
  id,
  registrations,
  isLoading,
  canModify,
}: {
  kind: TableSourceKind;
  id: string;
  registrations: TableRegistration[];
  isLoading: boolean;
  canModify: boolean;
}) {
  if (isLoading) {
    return <p className="text-xs text-muted-foreground">Loading&hellip;</p>;
  }
  if (registrations.length === 0) {
    return (
      <EmptyState icon={Table2} className="py-6">
        This file is not registered as a table yet.
      </EmptyState>
    );
  }
  return (
    <ul className="space-y-2">
      {registrations.map((reg) => (
        <RegistrationRow key={reg.id} kind={kind} id={id} reg={reg} canModify={canModify} />
      ))}
    </ul>
  );
}

// RegistrationRow renders one registered table: where to query it, what its
// columns are called, and the way to take it back out.
function RegistrationRow({
  kind,
  id,
  reg,
  canModify,
}: {
  kind: TableSourceKind;
  id: string;
  reg: TableRegistration;
  canModify: boolean;
}) {
  const unregister = useUnregisterTable(kind, id);

  return (
    <li
      data-testid={`table-registration-${reg.id}`}
      className="rounded-md border p-2.5 text-xs"
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0 flex-1">
          {/*
            The qualified name is what a reader came for and what they type
            into a query, so it wraps rather than truncating: the asset
            viewer's sidebar is narrow enough that "scratch.uploads..." was all
            of it a CSV asset ever showed. It has no spaces, so the break has
            to be allowed mid-token.
          */}
          <code className="block font-mono text-sm break-all text-foreground">{reg.query_table}</code>
          <p className="mt-0.5 text-muted-foreground">
            on <span className="font-medium text-foreground">{reg.connection}</span> · registered by{" "}
            {reg.registered_by}
          </p>
        </div>
        <div className="flex shrink-0 items-center gap-1">
          <CopyButton text={reg.query_table} label="Copy the table name" />
          {canModify && (
            <Button
              type="button"
              variant="ghost"
              size="xs"
              disabled={unregister.isPending}
              onClick={() => unregister.mutate(reg.id)}
              title="Drop this table"
              aria-label={`Drop ${reg.query_table}`}
            >
              {unregister.isPending ? <Loader2 className="animate-spin" /> : <Trash2 />}
            </Button>
          )}
        </div>
      </div>

      {reg.stale && (
        <Alert variant="warning" className="mt-2 py-2">
          <AlertTriangle />
          <AlertDescription>
            This file has a newer version than the table points at, so the table still returns the
            older one. Register it again to move the table to the current version.
          </AlertDescription>
        </Alert>
      )}

      {reg.columns.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1">
          {reg.columns.map((c) => (
            <Badge key={c.name} variant="muted" className="rounded px-1.5 font-mono">
              {c.name}
            </Badge>
          ))}
        </div>
      )}

      {unregister.isError && (
        <p className="mt-2 text-destructive">{errorText(unregister.error)}</p>
      )}
    </li>
  );
}

// RegisterForm is the register action: which connection, and what to call the
// table. The connection list is the only source of choices, so every option it
// offers is one the platform will accept.
function RegisterForm({
  kind,
  id,
  filename,
  connections,
  onDone,
}: {
  kind: TableSourceKind;
  id: string;
  filename?: string;
  connections: { name: string; description?: string; catalog: string; schema: string }[];
  onDone: () => void;
}) {
  const [connection, setConnection] = useState(connections[0]?.name ?? "");
  const [tableName, setTableName] = useState("");
  const register = useRegisterTable(kind, id);

  const target = connections.find((c) => c.name === connection);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    register.mutate(
      { connection, table_name: tableName.trim() || undefined },
      { onSuccess: onDone },
    );
  };

  return (
    <form onSubmit={submit} className="space-y-3 rounded-md border bg-muted/30 p-3">
      <div className="space-y-1.5">
        <Label htmlFor="table-connection" className="text-xs">
          Connection
        </Label>
        <Select value={connection} onValueChange={setConnection}>
          <SelectTrigger id="table-connection" size="sm" className="w-full">
            <SelectValue placeholder="Choose a connection" />
          </SelectTrigger>
          <SelectContent>
            {connections.map((c) => (
              <SelectItem key={c.name} value={c.name}>
                {c.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
        {target && (
          <p className="text-xs text-muted-foreground">
            The table is created in{" "}
            <code className="font-mono">
              {target.catalog}.{target.schema}
            </code>
            , a workspace everyone with this connection can read.
          </p>
        )}
      </div>

      <div className="space-y-1.5">
        <Label htmlFor="table-name" className="text-xs">
          Table name <span className="font-normal text-muted-foreground">(optional)</span>
        </Label>
        <Input
          id="table-name"
          value={tableName}
          onChange={(e) => setTableName(e.target.value)}
          placeholder={filename ? suggestName(filename) : "vendor_keys"}
          className="h-8 text-sm"
        />
        <p className="text-xs text-muted-foreground">
          Your persona is added as a prefix, so your table cannot collide with someone else&rsquo;s.
        </p>
      </div>

      {register.isError && (
        <Alert variant="destructive" className="py-2">
          <AlertTriangle />
          <AlertDescription>{errorText(register.error)}</AlertDescription>
        </Alert>
      )}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="ghost" size="sm" onClick={onDone}>
          Cancel
        </Button>
        <Button type="submit" size="sm" disabled={!connection || register.isPending}>
          {register.isPending && <Loader2 className="animate-spin" />}
          Register
        </Button>
      </div>
    </form>
  );
}

// suggestName renders the default table name the server would derive, so the
// placeholder shows what leaving the field empty produces rather than an
// unrelated example.
function suggestName(filename: string): string {
  const stem = filename.replace(/\.[^.]+$/, "");
  const slug = stem
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "_")
    .replace(/^_+|_+$/g, "");
  return slug || "table";
}

// errorText renders what the platform refused with. Its refusals name what to
// do next -- the object in the way, who holds the name -- so they are shown as
// written rather than replaced with a generic failure.
function errorText(err: unknown): string {
  if (err instanceof TableApiError) {
    return err.detail;
  }
  return err instanceof Error ? err.message : "The registration failed.";
}
