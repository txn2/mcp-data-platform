import { useState } from "react";
import { KeySquare, Trash2 } from "lucide-react";
import {
  useConnections,
  useDeleteSecret,
  usePersonas,
  usePutSecret,
  useSecret,
} from "@/api/admin/hooks";
import type { Secret } from "@/api/admin/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { ConfigField, ConfigGroup } from "@/pages/settings/connections/fields";
import {
  choices,
  EMPTY_FORM,
  fromSecret,
  placeholder,
  problems,
  type SecretForm,
  toggled,
  toInput,
} from "./secretForm";

// SecretEditor creates a secret, or changes one (#2051). The value is
// write-only: an edit shows an empty field, and leaving it empty keeps the
// stored value.

export function SecretEditor({
  name,
  onBack,
  onSaved,
}: {
  /** name is the secret being edited, or undefined to create one. */
  name?: string;
  onBack: () => void;
  onSaved: (name: string) => void;
}) {
  const existing = useSecret(name ?? "");
  if (name && existing.isLoading) {
    return (
      <div className="flex justify-center py-16">
        <LoadingIndicator />
      </div>
    );
  }
  if (name && (existing.error || !existing.data)) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel="Secrets" onBack={onBack} title={name} />
        <Alert variant="destructive" className="py-2">
          <AlertDescription>This secret could not be read.</AlertDescription>
        </Alert>
      </div>
    );
  }
  return (
    <EditorForm
      secret={name ? existing.data : undefined}
      onBack={onBack}
      onSaved={onSaved}
    />
  );
}

function EditorForm({
  secret,
  onBack,
  onSaved,
}: {
  secret?: Secret;
  onBack: () => void;
  onSaved: (name: string) => void;
}) {
  const creating = secret === undefined;
  const [form, setForm] = useState<SecretForm>(
    secret ? fromSecret(secret) : EMPTY_FORM,
  );
  const [shown, setShown] = useState<string[]>([]);
  const put = usePutSecret();
  const set =
    <K extends keyof SecretForm>(key: K) =>
    (value: SecretForm[K]) =>
      setForm((f) => ({ ...f, [key]: value }));

  const save = () => {
    const found = problems(form, creating);
    setShown(found);
    if (found.length > 0) return;
    put.mutate(
      { name: form.name, body: toInput(form) },
      { onSuccess: (s) => onSaved(s.name) },
    );
  };

  return (
    <div className="space-y-4">
      <PageHeader
        backLabel="Secrets"
        onBack={onBack}
        icon={KeySquare}
        title={secret ? secret.name : "New secret"}
        urn={secret ? placeholder(secret.name) : undefined}
        subtitle="A request names it as a placeholder; the platform fills in the value as the request is sent and redacts it from the response."
        actions={
          secret ? (
            <DeleteButton name={secret.name} onDeleted={onBack} />
          ) : undefined
        }
      />

      <ConfigGroup title="Secret">
        {creating && (
          <ConfigField
            label="Name"
            required
            mono
            value={form.name}
            onChange={set("name")}
            placeholder="portal_password"
            help={`A request references it as ${placeholder(form.name || "name")}.`}
          />
        )}
        <ConfigField
          label="Description"
          value={form.description}
          onChange={set("description")}
        />
        <ConfigField
          label={creating ? "Value" : "New value"}
          required={creating}
          sensitive
          value={form.value}
          onChange={set("value")}
          help={
            creating
              ? "Never shown again after it is saved."
              : "Leave empty to keep the stored value."
          }
        />
      </ConfigGroup>

      <ConnectionsGroup
        chosen={form.connections}
        onChange={set("connections")}
      />
      <PersonasGroup chosen={form.personas} onChange={set("personas")} />

      <Refusals shown={shown} error={put.error as Error | null} />
      <Actions
        creating={creating}
        saving={put.isPending}
        onCancel={onBack}
        onSave={save}
      />
    </div>
  );
}

/** Refusals shows what the form found, then what the platform answered. */
function Refusals({ shown, error }: { shown: string[]; error: Error | null }) {
  return (
    <>
      {shown.length > 0 && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>
            <ul className="list-disc pl-4">
              {shown.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
    </>
  );
}

function Actions({
  creating,
  saving,
  onCancel,
  onSave,
}: {
  creating: boolean;
  saving: boolean;
  onCancel: () => void;
  onSave: () => void;
}) {
  let label = creating ? "Create secret" : "Save changes";
  if (saving) label = "Saving...";
  return (
    <div className="flex justify-end gap-2">
      <Button
        type="button"
        variant="outline"
        onClick={onCancel}
        disabled={saving}
      >
        Cancel
      </Button>
      <Button type="button" onClick={onSave} disabled={saving}>
        {label}
      </Button>
    </div>
  );
}

function ConnectionsGroup({
  chosen,
  onChange,
}: {
  chosen: string[];
  onChange: (v: string[]) => void;
}) {
  const { data, isLoading } = useConnections();
  const known = (data?.connections ?? [])
    .filter((c) => c.kind === "api")
    .map((c) => c.name);
  return (
    <ConfigGroup title="Connections">
      <p className="text-xs text-muted-foreground">
        The API connections the secret may be sent through. A request on any
        other connection is refused before it is sent.
      </p>
      <Checklist
        label="Connections"
        names={choices(known, chosen)}
        chosen={chosen}
        onChange={onChange}
        empty={isLoading ? "Loading..." : "No API connection is configured."}
      />
    </ConfigGroup>
  );
}

function PersonasGroup({
  chosen,
  onChange,
}: {
  chosen: string[];
  onChange: (v: string[]) => void;
}) {
  const { data, isLoading } = usePersonas();
  const known = (data?.personas ?? []).map((p) => p.name);
  return (
    <ConfigGroup title="Personas">
      <p className="text-xs text-muted-foreground">
        The personas that may use the secret. With none chosen, any persona that
        can reach one of its connections may. The administrator persona always
        may.
      </p>
      <Checklist
        label="Personas"
        names={choices(known, chosen)}
        chosen={chosen}
        onChange={onChange}
        empty={isLoading ? "Loading..." : "No persona is defined."}
      />
    </ConfigGroup>
  );
}

function Checklist({
  label,
  names,
  chosen,
  onChange,
  empty,
}: {
  label: string;
  names: string[];
  chosen: string[];
  onChange: (v: string[]) => void;
  empty: string;
}) {
  if (names.length === 0)
    return <p className="text-sm text-muted-foreground">{empty}</p>;
  return (
    <fieldset aria-label={label} className="grid gap-1.5 sm:grid-cols-2">
      {names.map((n) => (
        <label key={n} className="flex items-center gap-2 text-sm">
          <input
            type="checkbox"
            className="size-4 accent-primary"
            checked={chosen.includes(n)}
            onChange={() => onChange(toggled(chosen, n))}
          />
          <span className="font-mono">{n}</span>
        </label>
      ))}
    </fieldset>
  );
}

function DeleteButton({
  name,
  onDeleted,
}: {
  name: string;
  onDeleted: () => void;
}) {
  const [open, setOpen] = useState(false);
  const remove = useDeleteSecret();
  return (
    <>
      <Button
        type="button"
        variant="outline"
        size="sm"
        onClick={() => setOpen(true)}
      >
        <Trash2 />
        Delete
      </Button>
      <ConfirmDialog
        open={open}
        onOpenChange={setOpen}
        title={`Delete ${name}?`}
        description={`A request that still names ${placeholder(name)} is refused and sends nothing.`}
        confirmLabel="Delete"
        destructive
        loading={remove.isPending}
        error={remove.error ? (remove.error as Error).message : undefined}
        onConfirm={async () => {
          await remove.mutateAsync(name);
          setOpen(false);
          onDeleted();
        }}
      />
    </>
  );
}
