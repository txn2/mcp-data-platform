import { useState } from "react";
import { useSiteDeployments, type Site, type SiteDeployment } from "@/api/portal/hooks/sites";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { orderedEnvironments, when } from "./siteFormat";

const ALL = "_:all";
const LABELS: Record<string, { label: string; variant: "info" | "success" | "warning" | "danger" | "muted" }> = {
  publish: { label: "Publish", variant: "info" },
  promote: { label: "Promote", variant: "success" },
  rollback: { label: "Roll back", variant: "warning" },
  application: { label: "Application release", variant: "muted" },
  offline: { label: "Taken offline", variant: "danger" },
  online: { label: "Back online", variant: "success" },
  redirect: { label: "Redirect", variant: "warning" },
  rename: { label: "Rename", variant: "muted" },
};

/** Whose decision moved an environment an agent asked to move (#2643). */
const DECIDED: Record<string, string> = {
  elicitation: "Accepted by the person when their agent asked",
  approval: "Approved by a person in Approvals",
  environment_setting: "Through Agents may promote here",
};

/** What a move said beside its release: a message, a target, an old name, whose decision. */
function details(d: SiteDeployment): string {
  const parts: string[] = [];
  if (d.action === "rename" && d.detail) parts.push(`Was ${d.detail}`);
  else if (d.action === "redirect" && d.detail) parts.push(`To ${d.detail}`);
  else if (d.action === "offline") parts.push(d.detail ? `"${d.detail}"` : "Default message");
  if (d.decided_by) parts.push(DECIDED[d.decided_by] ?? d.decided_by);
  return parts.join(". ");
}

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
                <TableHead>Details</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {data!.deployments.map((d, i) => {
                const l = LABELS[d.action] ?? { label: d.action, variant: "muted" as const };
                return (
                  <TableRow key={`${d.created_at}-${i}`}>
                    <TableCell>{when(d.created_at)}</TableCell>
                    <TableCell className="font-medium">
                      {d.environment}
                      {d.removed && (
                        <Badge variant="muted" className="ml-1">
                          Removed
                        </Badge>
                      )}
                    </TableCell>
                    <TableCell>
                      <Badge variant={l.variant}>{l.label}</Badge>
                    </TableCell>
                    <TableCell>{d.from_release > 0 && d.from_release !== d.to_release ? `${d.from_release} to ${d.to_release}` : d.to_release}</TableCell>
                    <TableCell>{d.actor}</TableCell>
                    <TableCell className="max-w-72 text-xs text-muted-foreground">{details(d)}</TableCell>
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
