import { useState } from "react";
import { KeySquare, Trash2 } from "lucide-react";
import {
  useConnections,
  useDeleteSecret,
  usePersonas,
  usePutSecret,
  useSecret,
  useSecretCode,
} from "@/api/admin/hooks";
import type { Secret, SecretKind } from "@/api/admin/types";
import { ConfirmDialog } from "@/components/ConfirmDialog";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { ConfigField, ConfigGroup, ConfigSelect } from "@/pages/settings/connections/fields";
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
// stored value. A secret is a value, or an authenticator seed whose current
// one-time code a request is sent (#2065).

const KINDS = [
  { value: "value", label: "Value" },
  { value: "totp", label: "Authenticator seed" },
];

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
    const found = problems(form, creating, secret?.kind);
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
        urn={secret ? placeholder(secret.name, secret.kind) : undefined}
        subtitle="A request names it as a placeholder; the platform fills in the value, or an authenticator seed's current code, as the request is sent."
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
            help={`A request references it as ${placeholder(form.name || "name", form.kind)}.`}
          />
        )}
        <ConfigField
          label="Description"
          value={form.description}
          onChange={set("description")}
        />
        <ValueFields form={form} creating={creating} set={set} />
      </ConfigGroup>

      {secret?.kind === "totp" && secret.totp && (
        <SeedGroup name={secret.name} params={secret.totp} />
      )}

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

/** ValueFields are the kind and the value or seed typed for it. */
function ValueFields({
  form,
  creating,
  set,
}: {
  form: SecretForm;
  creating: boolean;
  set: <K extends keyof SecretForm>(key: K) => (value: SecretForm[K]) => void;
}) {
  const seed = form.kind === "totp";
  return (
    <>
      <ConfigSelect
        label="Kind"
        value={form.kind}
        onChange={(v) => set("kind")(v as SecretKind)}
        options={KINDS}
        help={
          seed
            ? "The seed is never sent. A request writes {{totp:<name>}} and is sent the code for that moment, one request per period."
            : "Sent where a request writes {{secret:<name>}}, and redacted from what comes back."
        }
      />
      <ConfigField
        label={valueLabel(seed, creating)}
        required={creating}
        sensitive
        mono={seed}
        value={form.value}
        onChange={set("value")}
        placeholder={seed ? "otpauth://totp/Vendor:ops@example.com?secret=..." : undefined}
        help={valueHelp(seed, creating)}
      />
    </>
  );
}

/** valueLabel names the field the value or seed is typed into. */
function valueLabel(seed: boolean, creating: boolean): string {
  if (seed) return creating ? "Seed" : "New seed";
  return creating ? "Value" : "New value";
}

/** valueHelp says what the field takes and what saving it does. */
function valueHelp(seed: boolean, creating: boolean): string {
  const kept = creating ? "Never shown again after it is saved." : "Leave empty to keep the stored one.";
  if (!seed) return creating ? kept : "Leave empty to keep the stored value.";
  return `The otpauth://totp/... URI the provider's QR code holds, or the bare base32 seed. ${kept}`;
}

/** SeedGroup shows a stored seed's parameters, and its current code on request
 * so an administrator can compare it with the authenticator app. Reading the
 * code does not use it up: the next request may be sent the same one. */
function SeedGroup({ name, params }: { name: string; params: { algorithm: string; digits: number; period: number } }) {
  const [asked, setAsked] = useState(false);
  const current = useSecretCode(name, asked);
  return (
    <ConfigGroup title="Authenticator seed">
      <dl className="grid grid-cols-3 gap-3 text-sm">
        <div>
          <dt className="text-xs text-muted-foreground">Algorithm</dt>
          <dd className="font-mono">{params.algorithm}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Digits</dt>
          <dd className="font-mono">{params.digits}</dd>
        </div>
        <div>
          <dt className="text-xs text-muted-foreground">Period</dt>
          <dd className="font-mono">{params.period} seconds</dd>
        </div>
      </dl>
      <div className="flex items-center gap-3">
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={() => (asked ? void current.refetch() : setAsked(true))}
          disabled={current.isFetching}
        >
          Current code
        </Button>
        {current.data && (
          <span className="text-sm" data-testid="current-code">
            <span className="font-mono text-base font-semibold tracking-widest">{current.data.code}</span>{" "}
            <span className="text-muted-foreground">{current.data.seconds_left} seconds left</span>
          </span>
        )}
        {current.error && (
          <span className="text-sm text-destructive">{(current.error as Error).message}</span>
        )}
      </div>
    </ConfigGroup>
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

// SECRET_KINDS are the connection kinds a stored secret can be used by: an
// api connection's requests fill it (#2051), and an api, graphql or mcp
// connection's own configuration may name it (#2066).
const SECRET_KINDS = ["api", "graphql", "mcp"];

function ConnectionsGroup({
  chosen,
  onChange,
}: {
  chosen: string[];
  onChange: (v: string[]) => void;
}) {
  const { data, isLoading } = useConnections();
  const known = (data?.connections ?? [])
    .filter((c) => SECRET_KINDS.includes(c.kind))
    .map((c) => c.name);
  return (
    <ConfigGroup title="Connections">
      <p className="text-xs text-muted-foreground">
        The connections the secret may be used by: in an API connection&apos;s requests, and in the
        configuration of an API, GraphQL or MCP connection that names it. Any other use is refused
        before anything is sent.
      </p>
      <Checklist
        label="Connections"
        names={choices(known, chosen)}
        chosen={chosen}
        onChange={onChange}
        empty={isLoading ? "Loading..." : "No API, GraphQL or MCP connection is configured."}
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
