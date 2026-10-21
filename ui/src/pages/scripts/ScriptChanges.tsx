import { useState } from "react";
import { usePortalScriptVersions } from "@/api/portal/hooks/scripts";
import type { ScriptVersion } from "@/api/admin/types";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Button } from "@/components/ui/button";
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
//
// The newest few are shown and the rest wait behind "Show N more" (#2185): a
// script revised often would otherwise push its schedule, runs and outputs
// below the fold. Whether the list is open is remembered in this browser.
export function ScriptChanges({ scriptId }: { scriptId: string }) {
  const { data } = usePortalScriptVersions(scriptId, true);
  const [expanded, setExpanded] = useState(readExpanded);
  const changes = (data?.data ?? []).filter((v) => v.change_summary);
  if (changes.length === 0) return null;
  const hidden = Math.max(0, changes.length - SHOWN_CHANGES);
  const shown = expanded ? changes : changes.slice(0, SHOWN_CHANGES);
  function toggle() {
    setExpanded(!expanded);
    writeExpanded(!expanded);
  }
  return (
    <SectionCard title="What changed" data-testid="script-changes">
      <ul className="space-y-3">
        {shown.map((v) => (
          <ChangeRow key={v.id} version={v} />
        ))}
      </ul>
      {hidden > 0 && (
        <Button type="button" variant="ghost" size="xs" className="mt-2 px-1 text-muted-foreground" onClick={toggle}>
          {expanded ? "Show fewer" : `Show ${hidden} more`}
        </Button>
      )}
      <p className="mt-2 text-xs text-muted-foreground">
        Every version, with the code it changed, is listed under Source.
      </p>
    </SectionCard>
  );
}

// SHOWN_CHANGES is how many summaries, newest first, show before the toggle.
export const SHOWN_CHANGES = 3;

// EXPANDED_KEY remembers, in this browser, that the list was opened.
const EXPANDED_KEY = "script-changes-expanded";

function readExpanded(): boolean {
  try {
    return window.localStorage.getItem(EXPANDED_KEY) === "1";
  } catch {
    return false;
  }
}

function writeExpanded(open: boolean) {
  try {
    if (open) window.localStorage.setItem(EXPANDED_KEY, "1");
    else window.localStorage.removeItem(EXPANDED_KEY);
  } catch {
    // Storage blocked: the choice lasts as long as the page.
  }
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
