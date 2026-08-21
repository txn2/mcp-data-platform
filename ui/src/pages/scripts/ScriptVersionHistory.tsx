import { useState } from "react";
import { ChevronDown, ChevronRight } from "lucide-react";
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";
import type { ScriptVersion } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Badge } from "@/components/ui/badge";
import { SourceView } from "./DiffView";
import { formatWhen } from "./runFormat";

// ScriptVersionHistory is the owner's view of what has been written: every
// version with its author and the roles they held, which are the roles a run
// of that version presents. The version that runs — the latest saved one —
// opens by default, because "what is running right now" is the question this
// section is usually opened with.

export function ScriptVersionHistory({
  scriptId,
  contract,
}: {
  scriptId: string;
  contract: ScriptContract;
}) {
  const { data, isLoading, error } = usePortalScriptVersions(scriptId, true);
  const [openVersion, setOpenVersion] = useState<number | null>(contract.version);

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
              executing={v.version === contract.version}
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
            <Badge variant="muted">{version.status}</Badge>
            {executing && <Badge variant="success">Runs</Badge>}
          </div>
          <div className="text-xs text-muted-foreground">
            written by {version.author || "unknown"} on {formatWhen(version.created_at)}
          </div>
        </div>
        <span className="text-xs text-muted-foreground">
          {open ? "Hide source" : "Source"}
        </span>
      </div>
      {open && (
        <div className="mt-3 space-y-3">
          <AuthorityLine version={version} />
          <SourceView source={version.source} />
        </div>
      )}
    </>
  );
}

// AuthorityLine says what a run of this version presents: the roles its author
// held when they saved it, which the persona filter resolves at every call.
function AuthorityLine({ version }: { version: ScriptVersion }) {
  const roles = version.author_roles ?? [];
  return (
    <p className="text-xs text-muted-foreground">
      A run of this version presents{" "}
      <span className="font-mono">{roles.length > 0 ? roles.join(", ") : "no roles"}</span>
      {roles.length === 0
        ? " — it would resolve to the deny-all persona and could call nothing."
        : ", the roles its author held at the save."}
    </p>
  );
}
