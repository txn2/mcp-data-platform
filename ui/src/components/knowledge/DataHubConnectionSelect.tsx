import { useEffect } from "react";
import { Database } from "lucide-react";
import { useDataHubConnections } from "@/api/portal/datahub";

// DataHubConnectionSelect is the shared connection picker for the Catalog and
// Context Docs tabs (#719/#720). It lists the DataHub connections the persona can
// access (GET /datahub/connections), auto-selects the first, and flags read-only
// ones. When only one connection exists it still renders (as a labeled, disabled
// control) so the active connection is always visible.
export function DataHubConnectionSelect({
  value,
  onChange,
}: {
  value: string;
  onChange: (name: string) => void;
}) {
  const { data: connections, isLoading } = useDataHubConnections();

  // Default to the first connection once the list loads.
  useEffect(() => {
    if (!value && connections && connections.length > 0) {
      onChange(connections[0]!.name);
    }
  }, [connections, value, onChange]);

  if (isLoading) {
    return <div className="h-9 w-48 animate-pulse rounded-md bg-muted" />;
  }
  if (!connections || connections.length === 0) {
    return null;
  }

  const selected = connections.find((c) => c.name === value);

  return (
    <label className="flex items-center gap-2 text-sm">
      <Database className="h-4 w-4 text-muted-foreground" aria-hidden />
      <span className="text-muted-foreground">Connection</span>
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={connections.length === 1}
        aria-label="DataHub connection"
        className="rounded-md border bg-background px-2 py-1.5 text-sm outline-none ring-ring focus:ring-2 disabled:opacity-70"
      >
        {connections.map((c) => (
          <option key={c.name} value={c.name}>
            {c.name}
            {c.writable ? "" : " (read-only)"}
          </option>
        ))}
      </select>
      {selected && !selected.writable && (
        <span className="rounded bg-amber-500/10 px-1.5 py-0.5 text-xs font-medium text-amber-600 dark:text-amber-400">
          read-only
        </span>
      )}
    </label>
  );
}

// useConnectionWritable reports whether the named connection is write-enabled,
// so a tab can gate edit affordances on both the connection and the persona's
// tool grants.
export function useConnectionWritable(name: string): boolean {
  const { data: connections } = useDataHubConnections();
  return connections?.find((c) => c.name === name)?.writable ?? false;
}
