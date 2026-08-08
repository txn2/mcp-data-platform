import { useEffect } from "react";
import { Database } from "lucide-react";
import { useDataHubConnections } from "@/api/portal/datahub";
import { Badge } from "@/components/ui/badge";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";

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
    return <Skeleton className="h-9 w-48" />;
  }
  if (!connections || connections.length === 0) {
    return null;
  }

  const selected = connections.find((c) => c.name === value);

  return (
    <div className="flex items-center gap-2 text-sm">
      <Database className="size-4 text-muted-foreground" aria-hidden />
      <span className="text-muted-foreground">Connection</span>
      <Select value={value} onValueChange={onChange} disabled={connections.length === 1}>
        <SelectTrigger size="sm" aria-label="DataHub connection">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          {connections.map((c) => (
            <SelectItem key={c.name} value={c.name}>
              {c.name}
              {c.writable ? "" : " (read-only)"}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {selected && !selected.writable && <Badge variant="warning">read-only</Badge>}
    </div>
  );
}

// useConnectionWritable reports whether the named connection is write-enabled,
// so a tab can gate edit affordances on both the connection and the persona's
// tool grants.
export function useConnectionWritable(name: string): boolean {
  const { data: connections } = useDataHubConnections();
  return connections?.find((c) => c.name === name)?.writable ?? false;
}
