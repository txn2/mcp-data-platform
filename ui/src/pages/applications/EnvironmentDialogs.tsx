import { useState } from "react";
import {
  useAddEnvironment,
  usePromoteEnvironment,
  useRemoveEnvironment,
} from "@/api/portal/hooks/applicationEnvironments";
import type { ApplicationDetail } from "@/api/portal/hooks/applications";
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
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { errorText } from "./applicationFormat";
import { defaultSchema, envSchemaOf, lastEnvName, promoteItems, promotePlan } from "./environmentModel";
import { Field } from "./NewApplicationDialog";

// The dialogs an administrator changes an application's environments with
// (#2696, #2709): add one (on an application holding a schema, with the
// schema it runs in), promote a release one step with a list of what that
// does, and remove one.

const FIRST = "__first__";

/** AddEnvironmentDialog adds an environment; name prefills it (a site part's no-match row). */
export function AddEnvironmentDialog({
  detail,
  name: initialName = "",
  onClose,
}: {
  detail: ApplicationDetail;
  name?: string;
  onClose: () => void;
}) {
  const app = detail.application;
  const envs = app.environments ?? [];
  const add = useAddEnvironment(app.id);
  const [name, setName] = useState(initialName);
  const [schema, setSchema] = useState("");
  // The new one goes before the last, which /endpoints serves.
  const choices = envs.slice(0, -1).map((e) => e.name);
  const [after, setAfter] = useState(lastOf(choices));
  const [approval, setApproval] = useState(false);
  const [failure, setFailure] = useState<string | null>(null);
  const holdsSchema = app.schema !== "";
  const trimmed = name.trim();
  const last = lastEnvName(detail);
  const submit = () => {
    setFailure(null);
    const input = { name: trimmed, after, requires_approval: approval, ...schemaInput(holdsSchema, schema) };
    add.mutate(input, {
      onSuccess: onClose,
      onError: (e) => setFailure(errorText(e, "The environment could not be added")),
    });
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>Add environment</DialogTitle>
        </DialogHeader>
        <Field id="env-name" label="Name">
          <Input id="env-name" value={name} placeholder="stage" onChange={(e) => setName(e.target.value)} />
        </Field>
        <AfterField choices={choices} value={after} onChange={setAfter} />
        {holdsSchema && (
          <SchemaField connection={app.connection} last={last} value={schema} placeholder={defaultSchema(app.schema, trimmed || "stage")} onChange={setSchema} />
        )}
        <label className="flex min-h-11 items-center gap-2 text-sm">
          <input type="checkbox" checked={approval} onChange={(e) => setApproval(e.target.checked)} />
          Promotions here need approval
        </label>
        <p className="text-xs text-muted-foreground">It runs no release until one is promoted to it.</p>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={add.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={add.isPending || trimmed === ""}>
            Add
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** lastOf is the last of names, "" for none. */
function lastOf(names: string[]): string {
  return names.length > 0 ? names[names.length - 1]! : "";
}

/** AfterField is where a new environment goes: first, or after one before the last. */
function AfterField({ choices, value, onChange }: { choices: string[]; value: string; onChange: (v: string) => void }) {
  return (
    <Field id="env-after" label="After">
      <Select value={value || FIRST} onValueChange={(v) => onChange(v === FIRST ? "" : v)}>
        <SelectTrigger id="env-after" aria-label="After" className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={FIRST}>First (releases land here)</SelectItem>
          {choices.map((c) => (
            <SelectItem key={c} value={c}>
              {c}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
    </Field>
  );
}

/** schemaInput is the schema an add names, when one was typed for an application holding a schema. */
function schemaInput(holdsSchema: boolean, schema: string): { schema?: string } {
  return holdsSchema && schema.trim() ? { schema: schema.trim() } : {};
}

/** SchemaField is the schema a new environment runs in (#2709). */
function SchemaField({
  connection,
  last,
  value,
  placeholder,
  onChange,
}: {
  connection: string;
  last: string;
  value: string;
  placeholder: string;
  onChange: (v: string) => void;
}) {
  return (
    <Field id="env-schema" label="Schema" help={`On ${connection}, with its own run role. Built from ${last}'s structure, with no rows.`}>
      <Input id="env-schema" className="font-mono text-xs" value={value} placeholder={placeholder} onChange={(e) => onChange(e.target.value)} />
    </Field>
  );
}

/** PromoteDialog lists what promoting from's release into to does, then does it. */
export function PromoteDialog({
  detail,
  from,
  to,
  onClose,
}: {
  detail: ApplicationDetail;
  from: string;
  to: string;
  onClose: () => void;
}) {
  const promote = usePromoteEnvironment(detail.application.id);
  const [failure, setFailure] = useState<string | null>(null);
  const plan = promotePlan(detail, from, to);
  const items = promoteItems(plan, to);
  const submit = () => {
    setFailure(null);
    promote.mutate(
      { environment: from },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, `${from} could not be promoted`)) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent data-testid="promote-dialog">
        <DialogHeader>
          <DialogTitle>
            Promote release {plan.release} to {to}
          </DialogTitle>
          <DialogDescription>
            {plan.replaced > 0 ? `${to} runs release ${plan.replaced}.` : `${to} runs no release yet.`} Promoting release{" "}
            {plan.release}:
          </DialogDescription>
        </DialogHeader>
        <ul className="list-disc space-y-1 pl-5 text-sm" data-testid="promote-list">
          {items.length === 0 ? <li>changes nothing else</li> : items.map((i) => <li key={i}>{i}</li>)}
        </ul>
        <p className="text-xs text-muted-foreground">
          Data is not copied. {to} keeps its variables and its schema's rows; {from} keeps its own.
        </p>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={promote.isPending}>
            Cancel
          </Button>
          <Button onClick={submit} disabled={promote.isPending}>
            Promote
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** RemoveEnvironmentDialog removes an environment other than the last. */
export function RemoveEnvironmentDialog({
  detail,
  env,
  onClose,
}: {
  detail: ApplicationDetail;
  env: string;
  onClose: () => void;
}) {
  const app = detail.application;
  const remove = useRemoveEnvironment(app.id);
  const [failure, setFailure] = useState<string | null>(null);
  const own = envSchemaOf(detail, env);
  const kept = own && own.schema !== app.schema ? own.schema : "";
  const submit = () => {
    setFailure(null);
    remove.mutate(
      { environment: env },
      { onSuccess: onClose, onError: (e) => setFailure(errorText(e, `${env} could not be removed`)) },
    );
  };
  return (
    <Dialog open onOpenChange={(o) => !o && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>
            Remove environment {env} from {app.name}
          </DialogTitle>
          <DialogDescription>
            Its release and its variables go with it.
            {kept && ` The schema ${kept} is kept until an administrator drops it.`}
          </DialogDescription>
        </DialogHeader>
        {failure && (
          <Alert variant="destructive">
            <AlertDescription>{failure}</AlertDescription>
          </Alert>
        )}
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} disabled={remove.isPending}>
            Cancel
          </Button>
          <Button variant="destructive" onClick={submit} disabled={remove.isPending}>
            Remove
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
