import { useState } from "react";
import {
  useAddApplicationPart,
  useApplicationDatabases,
  type ApplicationDatabaseChoice,
  type ApplicationSchemaChoice,
} from "@/api/portal/hooks/applications";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { DialogFooter } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { errorText } from "./applicationFormat";

// AddSchemaPart is the Add parts dialog's Schema view (#2539): the
// application database schema an application holds is one of its parts,
// added by "<connection>/<schema>". The connections are the application
// database connections the caller's persona reaches, each with its schemas;
// a schema another application holds is shown and cannot be chosen, and a
// schema not listed is typed under its connection. An application holds one
// schema, so one that holds it already offers nothing to add. The server's
// refusal is shown as it says it.

interface Choice {
  connection: string;
  schema: string;
}

export function AddSchemaPart({
  app,
  heldSchema,
  onClose,
}: {
  app: string;
  /** The schema the application holds, "<connection>/<schema>", or null. */
  heldSchema: string | null;
  onClose: () => void;
}) {
  const databases = useApplicationDatabases(heldSchema === null);
  const add = useAddApplicationPart(app);
  const [choice, setChoice] = useState<Choice | null>(null);
  const [typed, setTyped] = useState<Record<string, string>>({});
  const [failure, setFailure] = useState<string | null>(null);
  const id = choice && choice.schema !== "" ? `${choice.connection}/${choice.schema}` : "";

  const type = (connection: string, value: string) => {
    setTyped((prev) => ({ ...prev, [connection]: value }));
    setChoice({ connection, schema: value.trim() });
  };

  const submit = () => {
    setFailure(null);
    add.mutate(
      { kind: "schema", id },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, "The schema could not be added")) },
    );
  };

  return (
    <>
      <div className="mt-4 flex max-h-[470px] flex-col gap-3 overflow-auto px-6" data-testid="schema-choices">
        {heldSchema !== null ? (
          <p className="py-6 text-sm text-muted-foreground">This application holds schema {heldSchema}.</p>
        ) : (
          <ConnectionList
            databases={databases.data}
            loading={databases.isLoading}
            error={databases.error}
            choice={choice}
            typed={typed}
            onChoose={setChoice}
            onType={type}
          />
        )}
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
      </div>
      <DialogFooter className="mt-4 flex-row items-center justify-between border-t px-6 py-4 sm:justify-between">
        <span className="truncate font-mono text-sm text-muted-foreground">{id}</span>
        <div className="flex gap-2.5">
          <Button variant="outline" onClick={onClose} disabled={add.isPending}>
            Cancel
          </Button>
          {heldSchema === null && (
            <Button onClick={submit} disabled={id === "" || add.isPending}>
              Add schema
            </Button>
          )}
        </div>
      </DialogFooter>
    </>
  );
}

function ConnectionList({
  databases,
  loading,
  error,
  choice,
  typed,
  onChoose,
  onType,
}: {
  databases?: ApplicationDatabaseChoice[];
  loading: boolean;
  error: Error | null;
  choice: Choice | null;
  typed: Record<string, string>;
  onChoose: (c: Choice) => void;
  onType: (connection: string, value: string) => void;
}) {
  if (loading) return <p className="py-6 text-sm text-muted-foreground">Loading…</p>;
  if (error) return <p className="py-6 text-sm text-destructive">{error.message}</p>;
  const list = databases ?? [];
  if (list.length === 0) {
    return (
      <p className="py-6 text-sm text-muted-foreground">Your persona reaches no application database connection.</p>
    );
  }
  return (
    <>
      {list.map((d) => (
        <div
          key={d.connection}
          role="group"
          aria-label={d.connection}
          data-testid={`schema-connection-${d.connection}`}
          className="flex flex-col gap-1 rounded-lg border p-3"
        >
          <div className="text-sm font-medium">
            {d.connection}
            {d.description && <span className="font-normal text-muted-foreground"> · {d.description}</span>}
          </div>
          {d.problem && <p className="text-xs text-destructive">{d.problem}</p>}
          {d.schemas.map((sc) => (
            <SchemaRow
              key={sc.name}
              connection={d.connection}
              schema={sc}
              checked={choice?.connection === d.connection && choice.schema === sc.name && !typed[d.connection]?.trim()}
              onChoose={() => {
                onType(d.connection, "");
                onChoose({ connection: d.connection, schema: sc.name });
              }}
            />
          ))}
          <Input
            aria-label="New schema name"
            placeholder="New schema name"
            className="mt-1"
            value={typed[d.connection] ?? ""}
            onChange={(e) => onType(d.connection, e.target.value)}
          />
        </div>
      ))}
    </>
  );
}

function SchemaRow({
  connection,
  schema,
  checked,
  onChoose,
}: {
  connection: string;
  schema: ApplicationSchemaChoice;
  checked: boolean;
  onChoose: () => void;
}) {
  const held = !!schema.held_by;
  const id = `schema-${connection}-${schema.name}`;
  return (
    <div className={cn("flex items-center gap-3 rounded-md px-2 py-1.5", checked && "bg-primary/10")}>
      <input
        id={id}
        type="radio"
        name="application-schema"
        className="size-4 accent-primary disabled:opacity-40"
        checked={checked}
        disabled={held}
        aria-label={held ? `${schema.name}, held by ${schema.held_by}` : schema.name}
        onChange={onChoose}
      />
      <label htmlFor={id} className={cn("flex min-w-0 flex-col", !held && "cursor-pointer")}>
        <span className={cn("truncate font-mono text-sm", held && "text-muted-foreground")}>{schema.name}</span>
        {held && <span className="text-xs text-muted-foreground">Held by {schema.held_by}</span>}
      </label>
    </div>
  );
}
