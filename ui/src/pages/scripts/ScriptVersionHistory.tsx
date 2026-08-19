import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import type { ScriptVersion } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { SourceView } from "./DiffView";
import { formatWhen } from "./runFormat";

// ScriptVersionHistory is the owner's view of what has been written and what
// was admitted: every version with its author, its approval provenance, and the
// capability grant approving it bound. The served version opens by default,
// because "what is running right now" is the question this section is usually
// opened with.

export function ScriptVersionHistory({
  scriptId,
  contract,
  onReview,
}: {
  scriptId: string;
  contract: ScriptContract;
  /**
   * onReview opens the decision surface for one version. It is present for an
   * administrator and absent for everyone else, which is the whole difference
   * between the two script surfaces (#1367): the sections are the same, and
   * approving is the one thing only an administrator does.
   */
  onReview?: (version: number) => void;
}) {
  const { data, isLoading, error } = usePortalScriptVersions(scriptId, true);
  const [openVersion, setOpenVersion] = useState<number | null>(
    contract.approval.approved ? (contract.approval.version ?? null) : null,
  );

  const versions = data?.data ?? [];

  return (
    <SectionCard title="Version history">
      {isLoading && <p className="text-sm text-muted-foreground">Loading history...</p>}
      {error && (
        <p className="text-sm text-muted-foreground">
          This script's history could not be loaded.
        </p>
      )}
      {!isLoading && !error && versions.length === 0 && (
        <p className="text-sm text-muted-foreground">This script has no versions.</p>
      )}
      <ul className="space-y-2">
        {versions.map((v) => (
          <li key={v.id} className="rounded-md border p-3">
            <VersionRow
              version={v}
              executing={v.version === contract.approval.version && contract.approval.approved}
              open={openVersion === v.version}
              onToggle={() => setOpenVersion(openVersion === v.version ? null : v.version)}
              onReview={onReview}
            />
          </li>
        ))}
      </ul>
    </SectionCard>
  );
}

function VersionRow({
  version,
  executing,
  open,
  onToggle,
  onReview,
}: {
  version: ScriptVersion;
  executing: boolean;
  open: boolean;
  onToggle: () => void;
  onReview?: (version: number) => void;
}) {
  return (
    <>
      {/* The header opens the version, with a chevron saying so — the same
          expander the portal uses for a block that is not a table row
          (settings/persona/primitives). */}
      <div
        role="button"
        tabIndex={0}
        aria-expanded={open}
        onClick={onToggle}
        onKeyDown={(e) => {
          if (e.key === "Enter" || e.key === " ") {
            e.preventDefault();
            onToggle();
          }
        }}
        className="flex cursor-pointer flex-wrap items-center justify-between gap-2 select-none"
      >
        <div className="min-w-0 space-y-1">
          <div className="flex flex-wrap items-center gap-1.5 text-sm">
            {open ? (
              <ChevronDown className="size-3.5 text-muted-foreground" />
            ) : (
              <ChevronRight className="size-3.5 text-muted-foreground" />
            )}
            <span className="font-mono">v{version.version}</span>
            <Badge variant={version.status === "draft" ? "warning" : "muted"}>
              {version.status}
            </Badge>
            {executing && <Badge variant="success">Executing</Badge>}
            {version.auto_approved && <Badge variant="muted">Nobody reviewed it</Badge>}
          </div>
          <div className="text-xs text-muted-foreground">
            written by {version.author || "unknown"} on {formatWhen(version.created_at)}
            {" · "}
            {approvalProvenance(version)}
          </div>
        </div>
        <span className="text-xs text-muted-foreground">
          {open ? "Hide source" : "Source and grant"}
        </span>
      </div>
      {open && (
        <div className="mt-3 space-y-3">
          <GrantSummary version={version} />
          <SourceView source={version.source} />
          {onReview && (
            <Button variant="outline" size="sm" onClick={() => onReview(version.version)}>
              {executing ? "Review the grant" : "Review this version"}
            </Button>
          )}
        </div>
      )}
    </>
  );
}

// approvalProvenance says who admitted this version, and separates a decision a
// person made from one the platform made for a personal script's own owner
// (#1367). approved_by names that owner either way, because they are
// accountable for the script; reading it as somebody's decision is the mistake
// this line exists to prevent.
function approvalProvenance(version: ScriptVersion): string {
  if (!version.approved_by) return "never approved";
  const when = formatWhen(version.approved_at);
  if (version.auto_approved) {
    return `approved automatically on ${when} because ${version.approved_by} owns this script and wrote it`;
  }
  return `approved by ${version.approved_by} on ${when}`;
}

// GrantSummary is what approving this version allowed it to reach. An
// unapproved version has no grant, and saying so is the point: it is not that
// the code may do nothing in particular, it is that nothing will run it.
function GrantSummary({ version }: { version: ScriptVersion }) {
  if (!version.approved_by) {
    return (
      <p className="text-xs text-muted-foreground">
        No grant is bound to this version, because nothing has approved it.
      </p>
    );
  }
  const destinations = version.grants.destinations.map((d) =>
    d.bucket ? `${d.name} (${d.bucket}/${d.prefix ?? ""})` : d.name,
  );
  return (
    <dl className="grid gap-x-6 gap-y-2 text-xs sm:grid-cols-2">
      <GrantFact label="Capabilities" values={version.grants.capabilities} />
      <GrantFact label="Connections" values={version.grants.connections} />
      <GrantFact label="Destinations" values={destinations} />
      <GrantFact label="Runs with the authority of" values={version.grants.roles} />
    </dl>
  );
}

function GrantFact({ label, values }: { label: string; values: string[] }) {
  return (
    <div className="min-w-0">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="font-mono break-words">{values.length > 0 ? values.join(", ") : "none"}</dd>
    </div>
  );
}
