import { useEffect } from "react";
import { Database } from "lucide-react";
import { useDataHubConnections } from "@/api/portal/datahub";

// DataHubConnectionSelect is the connection picker for the Catalog section
// (#719/#720/#1194), rendered once for the section rather than once per inner
// tab. It lists the DataHub connections the persona can access (GET
// /datahub/connections), selects the first when the current value names no
// connection it can see, and flags read-only ones. When only one connection
// exists it still renders (as a labeled, disabled control) so the active
// connection is always visible.
export function DataHubConnectionSelect({
  value,
  onChange,
}: {
  value: string;
  onChange: (name: string) => void;
}) {
  const { data: connections, isLoading } = useDataHubConnections();

  // Select the first connection once the list loads, whenever the current value
  // is not in it. That covers the empty first render and a persisted selection
  // whose connection has since been renamed, removed, or revoked: with a single
  // connection the control is disabled, so a stale value the reader cannot
  // change would otherwise wedge the whole section on 404s.
  useEffect(() => {
    if (connections && connections.length > 0 && !connections.some((c) => c.name === value)) {
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
