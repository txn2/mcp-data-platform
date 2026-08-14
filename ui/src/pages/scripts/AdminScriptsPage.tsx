import { useState } from "react";
import { FileCode2 } from "lucide-react";
import { useAdminScripts, useScriptReviews, useScriptVersions } from "@/api/admin/hooks";
import type { PendingReview, Script, ScriptVersion } from "@/api/admin/types";
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
import { ScriptReviewDrawer } from "./ScriptReviewDrawer";
import { ScriptReviewQueue } from "./ScriptReviewQueue";

// AdminScriptsPage is the managed-script review surface (#1287): the queue of
// versions waiting for a decision, every script and what it is executing, and
// the version history a rollback is approved from.
//
// It is the human control the execution gate has always needed. Approving is a
// REST call either way; what this page adds is a reviewer who can see what
// they are agreeing to.

// Open names the version the review drawer is open on.
interface Open {
  scriptID: string;
  scriptName: string;
  version: number;
}

export function AdminScriptsPage() {
  const reviews = useScriptReviews();
  const scripts = useAdminScripts();
  const [open, setOpen] = useState<Open | null>(null);
  const [expanded, setExpanded] = useState<Script | null>(null);

  const pending = reviews.data?.data ?? [];
  const allScripts = scripts.data?.data ?? [];

  const openReview = (row: PendingReview) =>
    setOpen({
      scriptID: row.script_id,
      scriptName: row.display_name || row.script_name,
      version: row.version,
    });

  return (
    <div className="space-y-4">
      {reviews.error && (
        <Alert variant="destructive">
          <AlertDescription>
            The review queue could not be loaded, so this page cannot say whether
            anything is waiting. The server may be unavailable.
          </AlertDescription>
        </Alert>
      )}

      <ScriptReviewQueue
        pending={pending}
        isLoading={reviews.isLoading}
        selectedVersionID={openQueueRow(pending, open)?.version_id ?? null}
        onOpen={openReview}
      />

      <SectionCard title="All scripts">
        <ScriptsSection
          scripts={allScripts}
          isLoading={scripts.isLoading}
          expanded={expanded}
          onExpand={setExpanded}
          onOpenVersion={(script, version) =>
            setOpen({
              scriptID: script.id,
              scriptName: script.display_name || script.name,
              version,
            })
          }
        />
      </SectionCard>

      {open && (
        <ScriptReviewDrawer
          key={`${open.scriptID}-${open.version}`}
          scriptID={open.scriptID}
          scriptName={open.scriptName}
          version={open.version}
          onClose={() => setOpen(null)}
        />
      )}
    </div>
  );
}

// ScriptsSection is the listing's three states: loading, nothing authored yet,
// and the table.
// openQueueRow finds the queue row the drawer is open on, matching the version
// as well as the script: a script can have a queued version and an older one
// open from its history, and highlighting the queued row then names a decision
// nobody is making.
function openQueueRow(pending: PendingReview[], open: Open | null): PendingReview | undefined {
  if (!open) return undefined;
  return pending.find((p) => p.script_id === open.scriptID && p.version === open.version);
}

function ScriptsSection({
  scripts,
  isLoading,
  expanded,
  onExpand,
  onOpenVersion,
}: {
  scripts: Script[];
  isLoading: boolean;
  expanded: Script | null;
  onExpand: (script: Script | null) => void;
  onOpenVersion: (script: Script, version: number) => void;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading scripts...</p>;
  }
  if (scripts.length === 0) {
    return (
      <EmptyState icon={FileCode2}>
        No scripts have been authored yet. An agent creates one through the manage_script
        tool; it appears here as soon as it needs a decision.
      </EmptyState>
    );
  }
  return (
    <ScriptTable
      scripts={scripts}
      expanded={expanded}
      onExpand={onExpand}
      onOpenVersion={onOpenVersion}
    />
  );
}

function ScriptTable({
  scripts,
  expanded,
  onExpand,
  onOpenVersion,
}: {
  scripts: Script[];
  expanded: Script | null;
  onExpand: (script: Script | null) => void;
  onOpenVersion: (script: Script, version: number) => void;
}) {
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Script</TableHead>
          <TableHead>Owner</TableHead>
          <TableHead>Executing</TableHead>
          <TableHead className="text-right">Versions</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {scripts.map((script) => (
          <ScriptRows
            key={script.id}
            script={script}
            expanded={expanded?.id === script.id}
            onExpand={onExpand}
            onOpenVersion={onOpenVersion}
          />
        ))}
      </TableBody>
    </Table>
  );
}

function ScriptRows({
  script,
  expanded,
  onExpand,
  onOpenVersion,
}: {
  script: Script;
  expanded: boolean;
  onExpand: (script: Script | null) => void;
  onOpenVersion: (script: Script, version: number) => void;
}) {
  return (
    <>
      <TableRow>
        <TableCell>
          <div className="font-medium">{script.display_name || script.name}</div>
          <div className="font-mono text-xs text-muted-foreground">{script.name}</div>
        </TableCell>
        <TableCell className="text-xs">{script.owner_email || "—"}</TableCell>
        <TableCell>
          <ExecutionState script={script} />
        </TableCell>
        <TableCell className="text-right">
          <Button
            size="sm"
            variant="outline"
            onClick={() => onExpand(expanded ? null : script)}
          >
            {expanded ? "Hide history" : "History"}
          </Button>
        </TableCell>
      </TableRow>
      {expanded && (
        <TableRow>
          <TableCell colSpan={4} className="bg-muted/30">
            <VersionHistory script={script} onOpenVersion={onOpenVersion} />
          </TableCell>
        </TableRow>
      )}
    </>
  );
}

// ExecutionState says what the script is running, which is the only status
// that matters on this page: a script with no approved version runs nothing,
// however healthy the rest of its record looks.
function ExecutionState({ script }: { script: Script }) {
  if (!script.approved_version_id) {
    return <Badge variant="muted">Nothing approved</Badge>;
  }
  if (!script.enabled || script.status === "deprecated") {
    return (
      <span className="flex flex-wrap items-center gap-1.5">
        <Badge variant="success">Approved</Badge>
        <Badge variant="muted">{script.enabled ? script.status : "disabled"}</Badge>
      </span>
    );
  }
  return <Badge variant="success">Approved v{script.version}</Badge>;
}

// VersionHistory is where a rollback happens: any version can be approved, and
// approving an earlier one points the execution gate back at it. The list says
// so rather than hiding the option behind the queue.
function VersionHistory({
  script,
  onOpenVersion,
}: {
  script: Script;
  onOpenVersion: (script: Script, version: number) => void;
}) {
  const { data, isLoading, error } = useScriptVersions(script.id);
  if (isLoading) return <p className="text-xs text-muted-foreground">Loading history...</p>;
  if (error) {
    return (
      <p className="text-xs text-muted-foreground">
        This script's history could not be loaded.
      </p>
    );
  }
  const versions = data?.data ?? [];
  if (versions.length === 0) {
    return <p className="text-xs text-muted-foreground">This script has no versions.</p>;
  }
  return (
    <ul className="space-y-1.5">
      {versions.map((v) => (
        <li key={v.id} className="flex items-center justify-between gap-3">
          <VersionLine version={v} executing={v.id === script.approved_version_id} />
          <Button size="sm" variant="outline" onClick={() => onOpenVersion(script, v.version)}>
            {v.id === script.approved_version_id ? "View" : "Review"}
          </Button>
        </li>
      ))}
    </ul>
  );
}

function VersionLine({ version, executing }: { version: ScriptVersion; executing: boolean }) {
  return (
    <div className="min-w-0 text-xs">
      <span className="font-mono">v{version.version}</span>{" "}
      <Badge variant={version.status === "draft" ? "warning" : "muted"}>
        {version.status}
      </Badge>{" "}
      {executing && <Badge variant="success">Executing</Badge>}{" "}
      <span className="text-muted-foreground">
        by {version.author || "unknown"}
        {version.approved_by && ` · approved by ${version.approved_by}`}
      </span>
    </div>
  );
}
