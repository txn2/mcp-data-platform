import { useId, useState } from "react";
import { useSetScriptExclusive } from "@/api/portal/hooks/scripts";
import type { ExclusiveMode, ScriptContract } from "@/api/portal/hooks/scripts";

// The three choices, each with the sentence that says what it does.
const choices: { mode: ExclusiveMode; label: string; help: string }[] = [
  {
    mode: "off",
    label: "Allowed",
    help: "Two runs of this script can execute together.",
  },
  {
    mode: "refuse",
    label: "Refused",
    help: "A run cannot start while another run of this script is pending or running.",
  },
  {
    mode: "queue",
    label: "Queued",
    help: "A run started while another run of this script is running waits its turn.",
  },
];

// ScriptExclusiveSetting is the owner's choice for overlapping runs (#1986,
// #2209). Refused: a scheduled fire while a run is open is skipped and a run
// asked for here or by an agent is refused naming the open run. Queued: such a
// run is accepted and waits until no other run of the script is running.
// Allowed: two runs of one script can execute together and only the state
// check at save time stands between them.
export function ScriptExclusiveSetting({
  scriptId,
  contract,
}: {
  scriptId: string;
  contract: ScriptContract;
}) {
  const save = useSetScriptExclusive(scriptId);
  const [error, setError] = useState<string | null>(null);
  const name = useId();
  const current: ExclusiveMode = contract.exclusive ?? "off";

  const choose = (next: ExclusiveMode) => {
    setError(null);
    save.mutate(next, {
      onError: (e) =>
        setError(
          e instanceof Error ? e.message : "The change could not be saved",
        ),
    });
  };

  return (
    <fieldset className="space-y-1 pb-3" disabled={save.isPending}>
      <legend className="text-sm font-medium">Overlapping runs</legend>
      {/* Native radios, as the persona editor's checkbox (#1980): no radio
          primitive is vendored. */}
      {choices.map((c) => (
        <label key={c.mode} className="flex items-start gap-2 text-sm">
          <input
            type="radio"
            className="mt-0.5"
            name={name}
            checked={current === c.mode}
            onChange={() => choose(c.mode)}
            aria-describedby={`${name}-${c.mode}`}
          />
          <span>
            <span className="font-medium">{c.label}</span>
            <span
              id={`${name}-${c.mode}`}
              className="block text-xs text-muted-foreground"
            >
              {c.help}
            </span>
          </span>
        </label>
      ))}
      {error && (
        <p role="alert" className="text-xs text-destructive">
          {error}
        </p>
      )}
    </fieldset>
  );
}
