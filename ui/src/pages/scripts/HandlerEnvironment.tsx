import { createContext, useContext } from "react";
import { Label } from "@/components/ui/label";

// The environment a schedule or webhook handler of an application's script
// runs in (#2696): the version that environment's release pins and its
// variables. A script no application holds offers none, and the field is
// not shown.

/** HandlerEnvironments are the environments of the application holding the script. */
export const HandlerEnvironments = createContext<string[]>([]);

/** LAST is the value that leaves the choice to the server: a new handler runs in the last environment. */
const LAST = "";

export function useHandlerEnvironments(): string[] {
  return useContext(HandlerEnvironments);
}

export function EnvironmentField({
  id,
  value,
  disabled,
  onChange,
}: {
  id: string;
  value: string;
  disabled?: boolean;
  onChange: (env: string) => void;
}) {
  const envs = useHandlerEnvironments();
  if (envs.length === 0) return null;
  const last = envs[envs.length - 1]!;
  return (
    <div className="space-y-1">
      <Label htmlFor={id}>Environment</Label>
      <select
        id={id}
        className="h-9 w-full max-w-sm rounded-md border bg-background px-2 text-sm"
        value={value || LAST}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value={LAST}>{last} (the last)</option>
        {envs.slice(0, -1).map((e) => (
          <option key={e} value={e}>
            {e}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">
        The release and variables its runs use.
      </p>
    </div>
  );
}
