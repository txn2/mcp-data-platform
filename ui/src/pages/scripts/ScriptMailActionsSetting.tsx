import { useId, useState } from "react";
import { useSetScriptMailActions } from "@/api/portal/hooks/scriptMailActions";
import type { ScriptContract } from "@/api/portal/hooks/scripts";

// ScriptMailActionsSetting is the owner's choice whether the script may act on
// mail it reads (#2247). A message is written by whoever sent it, so with this
// off a run that has read mail is refused every write after it.
export function ScriptMailActionsSetting({ scriptId, contract }: { scriptId: string; contract: ScriptContract }) {
  const save = useSetScriptMailActions(scriptId);
  const [error, setError] = useState<string | null>(null);
  const id = useId();
  const on = contract.mail_actions === true;

  const toggle = () => {
    setError(null);
    save.mutate(!on, {
      onError: (e) => setError(e instanceof Error ? e.message : "The change could not be saved"),
    });
  };

  return (
    <fieldset className="space-y-1 pb-3" disabled={save.isPending}>
      <legend className="text-sm font-medium">Mail</legend>
      <label className="flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-0.5" checked={on} onChange={toggle} aria-describedby={id} />
        <span>
          <span className="font-medium">Reads mail and acts on it</span>
          <span id={id} className="block text-xs text-muted-foreground">
            {on
              ? "This script may write after reading mail. A sender can influence what it does by what they write."
              : "A run that reads mail cannot write anything afterwards."}
          </span>
        </span>
      </label>
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </fieldset>
  );
}
