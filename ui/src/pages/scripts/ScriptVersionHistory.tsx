import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import type { ScriptVersion } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
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
}: {
  scriptId: string;
  contract: ScriptContract;
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
}: {
  version: ScriptVersion;
  executing: boolean;
  open: boolean;
  onToggle: () => void;
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
          </div>
          <div className="text-xs text-muted-foreground">
            written by {version.author || "unknown"} on {formatWhen(version.created_at)}
            {version.approved_by
              ? ` · approved by ${version.approved_by} on ${formatWhen(version.approved_at)}`
              : " · never approved"}
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
        </div>
      )}
    </>
  );
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
