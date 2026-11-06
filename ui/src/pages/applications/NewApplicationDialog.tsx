import { useState } from "react";
import {
  APPLICATION_NAME_RE,
  useApplicationDatabases,
  useCreateApplication,
  type Application,
  type ApplicationDatabaseChoice,
} from "@/api/portal/hooks/applications";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { errorText } from "./applicationFormat";

// The values the two selects use for "no database" and "a schema not yet
// created"; neither can be a connection or schema name, which never start
// with an underscore and a colon.
const NO_DATABASE = "_:none";
const NEW_SCHEMA = "_:new";

// NewApplicationDialog records an application (#2411): its name, what it is,
// and, when it has one, the application database schema it owns. The caller
// becomes its owner. The connection is chosen from the application database
// connections the caller's persona reaches, and the schema from that
// connection's schemas, a schema another application holds shown but not
// offered (#2491).
export function NewApplicationDialog({
  onClose,
  onCreated,
  onNavigate,
}: {
  onClose: () => void;
  onCreated: (a: Application) => void;
  /** Opens a portal path; the "About applications" link is shown only with it. */
  onNavigate?: (path: string) => void;
}) {
  const create = useCreateApplication();
  const databases = useApplicationDatabases();
  const form = useApplicationForm(databases.data);
  const [description, setDescription] = useState("");
  const [failure, setFailure] = useState<string | null>(null);
  const { trimmedName, hasDatabase, connection, schema } = form;

  const submit = () => {
    setFailure(null);
    create.mutate(
      {
        name: trimmedName,
        description: description.trim(),
        ...(hasDatabase ? { connection, schema } : {}),
      },
      {
        onSuccess: onCreated,
        onError: (e) =>
          setFailure(errorText(e, "The application could not be created")),
      },
    );
  };

  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>New application</DialogTitle>
          <Intro onNavigate={onNavigate} />
        </DialogHeader>
        <div className="space-y-3">
          <NameField value={form.name} invalid={form.nameInvalid} onChange={form.setName} />
          <Field
            id="application-description"
            label="Description"
            help="Optional."
          >
            <Textarea
              id="application-description"
              rows={3}
              value={description}
              aria-describedby="application-description-help"
              onChange={(e) => setDescription(e.target.value)}
            />
          </Field>
          <DatabaseField
            databases={databases.data}
            loaded={databases.isSuccess}
            value={connection}
            onChange={form.chooseConnection}
          />
          {hasDatabase && <SchemaField form={form} />}
          {failure && (
            <Alert variant="destructive">
              <AlertDescription>{failure}</AlertDescription>
            </Alert>
          )}
        </div>
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={create.isPending}>
            Cancel
          </Button>
          <Button
            onClick={submit}
            disabled={form.incomplete || create.isPending}
          >
            Create
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** chosenSchema is the schema the dialog names: none without a database, else the chosen or the new one. */
function chosenSchema(
  hasDatabase: boolean,
  choice: string,
  newSchema: string,
): string {
  if (!hasDatabase) return "";
  return choice === NEW_SCHEMA ? newSchema.trim() : choice;
}

function Intro({ onNavigate }: { onNavigate?: (path: string) => void }) {
  return (
    <DialogDescription>
      An application names the scripts, webhook sources and assets a system is
      built from, and can own one schema of an application database, whose
      migrations its releases apply. You become its owner. Each part belongs to
      one application.
      {onNavigate && (
        <>
          {" "}
          <button
            type="button"
            className="underline"
            onClick={() => onNavigate("/knowledge/pages/platform-applications")}
          >
            About applications
          </button>
        </>
      )}
    </DialogDescription>
  );
}

function DatabaseField({
  databases,
  loaded,
  value,
  onChange,
}: {
  databases?: ApplicationDatabaseChoice[];
  loaded: boolean;
  value: string;
  onChange: (value: string) => void;
}) {
  return (
    <>
      <Field
        id="application-connection"
        label="Database"
        help="An application database connection your persona reaches, or none for an application of scripts, sources and assets only."
      >
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger
            id="application-connection"
            className="w-full"
            aria-describedby="application-connection-help"
          >
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value={NO_DATABASE}>None (no database)</SelectItem>
            {(databases ?? []).map((d) => (
              <SelectItem key={d.connection} value={d.connection}>
                {d.connection}
                {d.description ? ` - ${d.description}` : ""}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      {loaded && (databases ?? []).length === 0 && (
        <p className="text-xs text-muted-foreground">
          Your persona reaches no application database connection.
        </p>
      )}
    </>
  );
}

function SchemaField({ form }: { form: ApplicationForm }) {
  const { chosen, schemaHeldBy, schemaChoice, newSchema } = form;
  return (
    <Field
      id="application-schema"
      label="Schema"
      help="A schema another application holds cannot be chosen. A new name is created by the first release."
      error={
        schemaHeldBy
          ? `Held by the application ${schemaHeldBy}.`
          : chosen?.problem
      }
    >
      <Select value={schemaChoice} onValueChange={form.setSchemaChoice}>
        <SelectTrigger
          id="application-schema"
          className="w-full"
          aria-describedby="application-schema-help"
        >
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={NEW_SCHEMA}>New schema</SelectItem>
          {(chosen?.schemas ?? []).map((sc) => (
            <SelectItem key={sc.name} value={sc.name} disabled={!!sc.held_by}>
              {sc.name}
              {sc.held_by ? ` (held by ${sc.held_by})` : ""}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      {schemaChoice === NEW_SCHEMA && (
        <Input
          aria-label="New schema name"
          className="mt-2"
          value={newSchema}
          placeholder={form.trimmedName.replace(/-/g, "_") || "schema_name"}
          onChange={(e) => form.setNewSchema(e.target.value)}
        />
      )}
    </Field>
  );
}

function NameField({
  value,
  invalid,
  onChange,
}: {
  value: string;
  invalid: boolean;
  onChange: (v: string) => void;
}) {
  return (
    <Field
      id="application-name"
      label="Name"
      help="Lowercase letters, digits and hyphens, starting with a letter; at most 63."
      error={
        invalid
          ? "Not a valid name: use lowercase letters, digits and hyphens, starting with a letter."
          : undefined
      }
    >
      <Input
        id="application-name"
        value={value}
        aria-invalid={invalid}
        aria-describedby="application-name-help"
        placeholder="order-system"
        onChange={(e) => onChange(e.target.value)}
      />
    </Field>
  );
}

type ApplicationForm = ReturnType<typeof useApplicationForm>;

// useApplicationForm holds the dialog's name, database and schema, and what
// they make: whether the name keeps the rule, the schema named, the
// application holding it, and whether Create has what it needs.
function useApplicationForm(databases?: ApplicationDatabaseChoice[]) {
  const [name, setName] = useState("");
  const [connection, setConnection] = useState(NO_DATABASE);
  const [schemaChoice, setSchemaChoice] = useState(NEW_SCHEMA);
  const [newSchema, setNewSchema] = useState("");
  const trimmedName = name.trim();
  const nameInvalid =
    trimmedName !== "" && !APPLICATION_NAME_RE.test(trimmedName);
  const chosen = databases?.find((d) => d.connection === connection);
  const hasDatabase = connection !== NO_DATABASE;
  const schema = chosenSchema(hasDatabase, schemaChoice, newSchema);
  const schemaHeldBy = chosen?.schemas.find(
    (sc) => sc.name === schema,
  )?.held_by;
  const incomplete =
    trimmedName === "" ||
    nameInvalid ||
    (hasDatabase && (schema === "" || !!schemaHeldBy));
  const chooseConnection = (value: string) => {
    setConnection(value);
    setSchemaChoice(NEW_SCHEMA);
    setNewSchema("");
  };
  return {
    name,
    setName,
    connection,
    chooseConnection,
    schemaChoice,
    setSchemaChoice,
    newSchema,
    setNewSchema,
    trimmedName,
    nameInvalid,
    chosen,
    hasDatabase,
    schema,
    schemaHeldBy,
    incomplete,
  };
}

export function Field({
  id,
  label,
  help,
  error,
  children,
}: {
  id: string;
  label: string;
  help?: string;
  error?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="space-y-1">
      <Label htmlFor={id} className="text-xs text-muted-foreground">
        {label}
      </Label>
      {children}
      {error ? (
        <p id={`${id}-help`} className="text-xs text-destructive">
          {error}
        </p>
      ) : (
        help && (
          <p id={`${id}-help`} className="text-xs text-muted-foreground">
            {help}
          </p>
        )
      )}
    </div>
  );
}
