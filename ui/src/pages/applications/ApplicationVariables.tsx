import { useMemo, useState } from "react";
import {
  useApplicationVariables,
  useDeleteVariable,
  useSetVariable,
  type ApplicationVariable,
} from "@/api/portal/hooks/applicationEnvironments";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
import { compareOrdinal } from "@/lib/sortByName";
import { SectionCard } from "@/components/patterns/SectionCard";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { errorText } from "./applicationFormat";
import { Field } from "./NewApplicationDialog";

// ApplicationVariables is NAME=value per environment (#2696): a row per name,
// a column for the application-wide default and one per environment. A
// script reads them with platform.env("NAME"); a change applies to the next
// run with no release. A value is plain text; a stored secret is referenced
// as {{secret:<name>}} and filled only when a request is sent through a
// connection the secret allows. The owner and administrators see and change
// them; nobody else is shown the section.

const DEFAULT = "";
const SECRET_REF = /^\{\{secret:[^}]+\}\}$/;

interface Props {
  detail: ApplicationDetail;
  canEdit: boolean;
}

interface Editing {
  name: string;
  environment: string;
  value: string;
  isNew: boolean;
}

export function ApplicationVariables({ detail, canEdit }: Props) {
  const app = detail.application;
  const envs = (app.environments ?? []).map((e) => e.name);
  const { data, error } = useApplicationVariables(app.id, canEdit);
  const [editing, setEditing] = useState<Editing | null>(null);
  const rows = useMemo(() => byName(data?.variables ?? []), [data]);
  if (!canEdit || envs.length === 0) return null;
  const columns = [DEFAULT, ...envs];
  return (
    <SectionCard
      id="application-variables"
      title="Variables"
      action={
        <Button
          size="sm"
          variant="outline"
          onClick={() =>
            setEditing({
              name: "",
              environment: DEFAULT,
              value: "",
              isNew: true,
            })
          }
        >
          Add variable
        </Button>
      }
    >
      <div className="space-y-3">
        <p className="text-sm text-muted-foreground">
          Scripts read these with platform.env(&quot;NAME&quot;). A change
          applies to the next run; no release is needed. To use a stored secret,
          write {"{{secret:<name>}}"}.
        </p>
        {error && (
          <Alert variant="destructive">
            <AlertDescription>{error.message}</AlertDescription>
          </Alert>
        )}
        {rows.length === 0 ? (
          <p className="text-sm text-muted-foreground">No variables</p>
        ) : (
          <div className="overflow-x-auto rounded-md border">
            <table
              className="w-full text-sm"
              data-testid="application-variables"
            >
              <thead>
                <tr className="border-b bg-muted/40 text-left text-xs text-muted-foreground">
                  <th className="px-3 py-2 font-medium">Name</th>
                  {columns.map((c) => (
                    <th key={c || "default"} className="px-3 py-2 font-medium">
                      {c || "Default"}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map(([name, values]) => (
                  <tr key={name} className="border-b last:border-0">
                    <td className="px-3 py-2 font-mono text-xs">{name}</td>
                    {columns.map((c) => (
                      <td key={c || "default"} className="px-3 py-2">
                        <button
                          type="button"
                          className="min-h-8 w-full truncate text-left hover:underline"
                          aria-label={`Edit ${name} for ${c || "the default"}`}
                          onClick={() =>
                            setEditing({
                              name,
                              environment: c,
                              value: values.get(c) ?? "",
                              isNew: !values.has(c),
                            })
                          }
                        >
                          <Cell
                            value={values.get(c)}
                            inherited={c !== DEFAULT && values.has(DEFAULT)}
                          />
                        </button>
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>
      {editing && (
        <EditVariableDialog
          app={app.id}
          envs={envs}
          editing={editing}
          onClose={() => setEditing(null)}
        />
      )}
    </SectionCard>
  );
}

function Cell({
  value,
  inherited,
}: {
  value: string | undefined;
  inherited: boolean;
}) {
  if (value === undefined) {
    return (
      <span className="text-muted-foreground">
        {inherited ? "default" : "unset"}
      </span>
    );
  }
  if (SECRET_REF.test(value)) {
    return (
      <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
        {value}
      </span>
    );
  }
  return <span className="font-mono text-xs">{value}</span>;
}

// byName is the variables grouped by name, each name's values by environment.
function byName(vars: ApplicationVariable[]): [string, Map<string, string>][] {
  const out = new Map<string, Map<string, string>>();
  for (const v of vars) {
    if (!out.has(v.name)) out.set(v.name, new Map());
    out.get(v.name)!.set(v.environment, v.value);
  }
  return [...out.entries()].sort(([a], [b]) => compareOrdinal(a, b));
}

function EditVariableDialog({
  app,
  envs,
  editing,
  onClose,
}: {
  app: string;
  envs: string[];
  editing: Editing;
  onClose: () => void;
}) {
  const set = useSetVariable(app);
  const del = useDeleteVariable(app);
  const [name, setName] = useState(editing.name);
  const [environment, setEnvironment] = useState(editing.environment);
  const [value, setValue] = useState(editing.value);
  const [failure, setFailure] = useState<string | null>(null);
  const busy = set.isPending || del.isPending;
  const fail = (what: string) => (e: unknown) => setFailure(errorText(e, what));
  const DEFAULT_ITEM = "__default__";
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            {editing.isNew ? "Add variable" : "Edit variable"}
          </DialogTitle>
        </DialogHeader>
        <Field id="var-name" label="Name">
          <Input
            id="var-name"
            className="font-mono"
            value={name}
            placeholder="API_BASE"
            disabled={editing.name !== ""}
            onChange={(e) => setName(e.target.value.toUpperCase())}
          />
        </Field>
        <Field id="var-env" label="Environment">
          <Select
            value={environment || DEFAULT_ITEM}
            onValueChange={(v) =>
              setEnvironment(v === DEFAULT_ITEM ? DEFAULT : v)
            }
          >
            <SelectTrigger
              id="var-env"
              aria-label="Environment"
              className="w-full"
            >
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value={DEFAULT_ITEM}>
                Default (every environment)
              </SelectItem>
              {envs.map((e) => (
                <SelectItem key={e} value={e}>
                  {e}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </Field>
        <Field id="var-value" label="Value">
          <Input
            id="var-value"
            className="font-mono"
            value={value}
            onChange={(e) => setValue(e.target.value)}
          />
        </Field>
        <p className="text-xs text-muted-foreground">
          Plain text, shown to the application&apos;s owner and administrators.
          Never paste a secret here: write {"{{secret:<name>}}"} to use one from
          Admin &gt; Secrets; it is filled when a request is sent and never
          shown to the script.
        </p>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter className="gap-2">
          {!editing.isNew && (
            <Button
              variant="outline"
              disabled={busy}
              onClick={() =>
                del.mutate(
                  { name, environment },
                  {
                    onSuccess: onClose,
                    onError: fail("The variable could not be removed"),
                  },
                )
              }
            >
              {environment === DEFAULT
                ? "Remove"
                : `Use the default for ${environment}`}
            </Button>
          )}
          <Button variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button
            disabled={busy || name.trim() === ""}
            onClick={() =>
              set.mutate(
                { name: name.trim(), environment, value },
                {
                  onSuccess: onClose,
                  onError: fail("The variable could not be saved"),
                },
              )
            }
          >
            Save
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
