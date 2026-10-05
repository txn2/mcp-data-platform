import { useId, useState } from "react";
import { useSetScriptExclusive } from "@/api/portal/hooks/scripts";
import type { ScriptContract } from "@/api/portal/hooks/scripts";

// ScriptExclusiveSetting is the owner's choice that a script runs one at a time
// (#1986). With it set, a scheduled fire while a run is open is skipped and a
// run asked for here or by an agent is refused naming the open run; without
// it, two runs of one script can execute together and only the state check at
// save time stands between them.
export function ScriptExclusiveSetting({
  scriptId,
  contract,
}: {
  scriptId: string;
  contract: ScriptContract;
}) {
  const save = useSetScriptExclusive(scriptId);
  const [error, setError] = useState<string | null>(null);
  const helpId = useId();
  const checked = Boolean(contract.exclusive);

  const toggle = (next: boolean) => {
    setError(null);
    save.mutate(next, {
      onError: (e) =>
        setError(
          e instanceof Error ? e.message : "The change could not be saved",
        ),
    });
  };

  return (
    <div className="space-y-1 pb-3">
      {/* A native checkbox, as the persona editor's (#1980): no checkbox
          primitive is vendored. */}
      <label className="flex items-start gap-2 text-sm">
        <input
          type="checkbox"
          className="mt-0.5"
          checked={checked}
          disabled={save.isPending}
          onChange={(e) => toggle(e.target.checked)}
          aria-describedby={helpId}
        />
        <span>
          <span className="font-medium">One run at a time</span>
          <span id={helpId} className="block text-xs text-muted-foreground">
            A run cannot start while another run of this script is pending or
            running.
          </span>
        </span>
      </label>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </div>
  );
}
