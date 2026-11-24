import { Link2 } from "lucide-react";
import { useSystemInfo } from "@/api/admin/hooks";
import { SettingsCard } from "./panels";

// ReferencesCard shows the scheme the platform writes references under
// (#2776). It is file configuration, server.reference_scheme, so the card
// only reads it.
export function ReferencesCard() {
  const { data: info, isLoading } = useSystemInfo();
  const scheme = info?.reference_scheme || "mcp";
  return (
    <SettingsCard
      icon={Link2}
      title="References"
      description="What every reference the platform hands to people and agents starts with"
    >
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <dl className="space-y-3 text-sm" data-testid="reference-scheme">
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <dt className="text-muted-foreground">Scheme</dt>
            <dd className="font-mono font-medium">{scheme}</dd>
          </div>
          <div className="flex flex-wrap items-baseline gap-x-3 gap-y-1">
            <dt className="text-muted-foreground">Example</dt>
            <dd className="font-mono">{scheme}:asset:&lt;id&gt;</dd>
          </div>
          <p className="text-muted-foreground">
            References written under <span className="font-mono">mcp:</span> keep working
            {scheme !== "mcp" ? (
              <>
                {" "}alongside <span className="font-mono">{scheme}:</span>
              </>
            ) : null}
            . The scheme is set in the configuration file as{" "}
            <span className="font-mono">server.reference_scheme</span>.
          </p>
        </dl>
      )}
    </SettingsCard>
  );
}
