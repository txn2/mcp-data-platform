import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
import type { ScriptVersion } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { formatWhen } from "./runFormat";

// ScriptChanges is what each version of an automation does differently, in
// the words the person it runs for agreed to (#1943). A version whose save
// changed what the automation produces or reaches carries a plain-language
// summary, agreed before it was saved (#1942); this is where that summary is
// read, by someone who wants to know what changed without reading code, a
// diff, lint output or test results -- none of which are shown here.
//
// A script none of whose versions changed behavior shows nothing: an empty
// section on every script would be read past, and the version history below
// already says who wrote each version.
export function ScriptChanges({ scriptId }: { scriptId: string }) {
  const { data } = usePortalScriptVersions(scriptId, true);
  const changes = (data?.data ?? []).filter((v) => v.change_summary);
  if (changes.length === 0) return null;
  return (
    <SectionCard title="What changed" data-testid="script-changes">
      <ul className="space-y-3">
        {changes.map((v) => (
          <ChangeRow key={v.id} version={v} />
        ))}
      </ul>
    </SectionCard>
  );
}

function ChangeRow({ version }: { version: ScriptVersion }) {
  return (
    <li className="space-y-1 border-l-2 border-primary/40 pl-3">
      <p className="text-sm">{version.change_summary}</p>
      <p className="text-xs text-muted-foreground">
        <span className="font-mono">v{version.version}</span>, saved by{" "}
        {version.author || "unknown"} on {formatWhen(version.created_at)}
        {version.change_agreed_at && (
          <>
            {"; "}agreed on {formatWhen(version.change_agreed_at)}
            {version.change_agreed_by && version.change_agreed_by !== version.author
              ? ` (confirmed by ${version.change_agreed_by})`
              : ""}
          </>
        )}
      </p>
    </li>
  );
}
