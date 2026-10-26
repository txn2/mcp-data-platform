import { usePersonas } from "@/api/admin/hooks";
import type { ScriptEndpoint } from "@/api/portal/hooks/scriptEndpoint";
import { Label } from "@/components/ui/label";

// formStorage says where a form's submissions are stored (#2178).
export function formStorage(table: string, connection: string): string {
  return `Stored in ${table} on ${connection}`;
}

// FormStorageFields are where a form's submissions are stored (#2178) and
// the personas a script may decrypt its sensitive fields as (#2179). The
// connection is chosen once, when the form's source is made.
export function FormStorageFields({
  endpoint,
  connection,
  decryptPersonas,
  onConnection,
  onDecryptPersonas,
}: {
  endpoint: ScriptEndpoint;
  connection: string;
  decryptPersonas: string[];
  onConnection: (v: string) => void;
  onDecryptPersonas: (v: string[]) => void;
}) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <div className="space-y-1">
        <Label htmlFor="endpoint-form-connection">Connection</Label>
        {endpoint.form ? (
          <p id="endpoint-form-connection" className="text-sm">
            {formStorage(endpoint.form.table, endpoint.form.connection)}
          </p>
        ) : (
          <ConnectionChoice choices={endpoint.form_connections ?? []} value={connection} onChange={onConnection} />
        )}
      </div>
      <DecryptPersonas chosen={decryptPersonas} onChange={onDecryptPersonas} />
    </div>
  );
}

function ConnectionChoice({ choices, value, onChange }: { choices: string[]; value: string; onChange: (v: string) => void }) {
  return (
    <>
      <select
        id="endpoint-form-connection"
        className="h-9 w-full rounded-md border bg-background px-2 text-sm"
        value={value}
        onChange={(e) => onChange(e.target.value)}
      >
        <option value="">Choose a connection</option>
        {choices.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">
        {choices.length === 0
          ? "No Trino connection has a scratch catalog that accepts writes, so submissions cannot be stored."
          : "The Trino connection whose scratch catalog holds the table each accepted submission is stored in."}
      </p>
    </>
  );
}

function DecryptPersonas({ chosen, onChange }: { chosen: string[]; onChange: (v: string[]) => void }) {
  const { data, isLoading } = usePersonas();
  const known = (data?.personas ?? []).map((p) => p.name);
  const names = [...new Set([...known, ...chosen])].sort();
  const toggle = (n: string) => onChange(chosen.includes(n) ? chosen.filter((x) => x !== n) : [...chosen, n]);
  return (
    <fieldset className="space-y-1">
      <legend className="text-sm font-medium">Decrypt personas</legend>
      {names.length === 0 ? (
        <p className="text-sm text-muted-foreground">{isLoading ? "Loading..." : "No persona is defined."}</p>
      ) : (
        <div className="grid gap-1.5 pt-1 sm:grid-cols-2">
          {names.map((n) => (
            <label key={n} className="flex items-center gap-2 text-sm">
              <input type="checkbox" checked={chosen.includes(n)} onChange={() => toggle(n)} />
              <span className="font-mono">{n}</span>
            </label>
          ))}
        </div>
      )}
      <p className="text-xs text-muted-foreground">
        The personas a script may decrypt the form&apos;s sensitive fields as. The administrator always may.
      </p>
    </fieldset>
  );
}
