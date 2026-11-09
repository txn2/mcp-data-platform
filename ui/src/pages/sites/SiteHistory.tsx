import { useState } from "react";
import { useSiteDeployments, type Site } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { orderedEnvironments, when } from "./siteFormat";

const ALL = "_:all";
const LABELS: Record<string, { label: string; variant: "info" | "success" | "warning" | "muted" }> = {
  publish: { label: "Publish", variant: "info" },
  promote: { label: "Promote", variant: "success" },
  rollback: { label: "Roll back", variant: "warning" },
  application: { label: "Application release", variant: "muted" },
};

// SiteHistoryTab is every move of the site's environments (#2559).
export function SiteHistoryTab({ site }: { site: Site }) {
  const [env, setEnv] = useState(ALL);
  const { data, isLoading, error } = useSiteDeployments(site.name, env === ALL ? "" : env);
  return (
    <SectionCard
      title="History"
      action={
        <Select value={env} onValueChange={setEnv}>
          <SelectTrigger className="w-48" aria-label="Environment">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={ALL}>All environments</SelectItem>
            {orderedEnvironments(site.environments).map((e) => (
              <SelectItem key={e.name} value={e.name}>
                {e.name}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      }
    >
      {error ? (
        <Alert variant="destructive">
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      ) : isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (data?.deployments.length ?? 0) === 0 ? (
        <p className="text-sm text-muted-foreground">Nothing has moved yet.</p>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>When</TableHead>
                <TableHead>Environment</TableHead>
                <TableHead>Action</TableHead>
                <TableHead>Release</TableHead>
                <TableHead>By</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data!.deployments.map((d, i) => {
                const l = LABELS[d.action] ?? { label: d.action, variant: "muted" as const };
                return (
                  <TableRow key={`${d.created_at}-${i}`}>
                    <TableCell>{when(d.created_at)}</TableCell>
                    <TableCell className="font-medium">{d.environment}</TableCell>
                    <TableCell>
                      <Badge variant={l.variant}>{l.label}</Badge>
                    </TableCell>
                    <TableCell>{d.from > 0 ? `${d.from} to ${d.to}` : d.to}</TableCell>
                    <TableCell>{d.actor}</TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}
    </SectionCard>
  );
}
