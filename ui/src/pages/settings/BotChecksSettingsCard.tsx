import { useCallback, useState } from "react";
import { ShieldCheck } from "lucide-react";
import { useBotChecks, useDeleteBotCheck, usePutBotCheck, usePutSecret, useSecrets } from "@/api/admin/hooks";
import type { BotCheck, BotCheckProvider } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { PROVIDER_LABELS } from "../scripts/endpoints/endpointLabels";
import { BOT_CHECK_USE, botCheckSecretInput, isBotCheckSecret, newSecretProblems, type NewSecret } from "./botCheckSecret";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners, UpdatedByMeta } from "./settingsChrome";
import { useSettingsDraft } from "./useSettingsDraft";
import { byName } from "@/lib/sortByName";

// BotChecksSettingsCard is the list of named bot checks a bot_check endpoint
// picks from (#2173, #2259): each the platform's own proof of work, or
// Cloudflare Turnstile or hCaptcha with the stored secret its key is kept in.
// A check an endpoint names cannot be deleted. An endpoint that wants no
// check uses the public access mode rather than a check.

const PROVIDERS = Object.keys(PROVIDER_LABELS) as BotCheckProvider[];

/** NEW_SECRET is the Secret option that stores a new secret with the check. */
const NEW_SECRET = "__new_secret__";

/** NAME_PATTERN is the name a check may have, as the server checks it. */
const NAME_PATTERN = /^[a-z0-9][a-z0-9_-]{0,62}$/;

/** fresh is a new check at the proof of work's defaults, as the server makes one. */
function fresh(): BotCheck {
  return {
    name: "",
    provider: "pow",
    site_key: "",
    secret_name: "",
    difficulty: 16,
    max_difficulty: 22,
    rate_threshold: 30,
    challenge_ttl_seconds: 300,
    used_by: [],
    usable: true,
  };
}

export function BotChecksSettingsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data, isLoading } = useBotChecks();
  // selected is the name of the check being edited, "" for a new one, and
  // null while none is open.
  const [selected, setSelected] = useState<string | null>(null);
  // blank is the new check's starting value, made once each time New is
  // pressed: the editor's draft resets whenever the value it is handed
  // changes, so a fresh object each render would wipe what was typed.
  const [blank, setBlank] = useState<BotCheck>(fresh);
  const checks = data?.checks ?? [];
  const open = selected === "" ? blank : checks.find((c) => c.name === selected);
  const startNew = () => {
    setBlank(fresh());
    setSelected("");
  };

  return (
    <SettingsCard
      icon={ShieldCheck}
      title="Bot checks"
      description="The checks a bot_check endpoint can require. Proof of work is the platform's own and calls no third party: it makes each request cost work, which any program can pay, so it slows an automated caller rather than stopping one. Turnstile and hCaptcha need a browser and are verified on the server with a key kept as a stored secret. A partner system calls an authenticated endpoint with an API key instead."
      action={
        !isReadOnly && (
          <Button size="sm" variant="outline" onClick={startNew}>
            New bot check
          </Button>
        )
      }
    >
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="space-y-5">
          {data && !data.key_available && (
            <Alert>
              <AlertDescription>
                This deployment has no browser-session signing key (auth.browser_session.signing_key), so it cannot
                sign a proof-of-work challenge, and no endpoint can be bound to a proof-of-work check.
              </AlertDescription>
            </Alert>
          )}
          <ChecksTable checks={checks} selected={selected} onSelect={setSelected} />
          {open && (
            <CheckEditor
              key={selected ?? ""}
              check={open}
              isNew={selected === ""}
              isReadOnly={isReadOnly}
              onDone={(name) => setSelected(name)}
            />
          )}
        </div>
      )}
    </SettingsCard>
  );
}

function ChecksTable({
  checks,
  selected,
  onSelect,
}: {
  checks: BotCheck[];
  selected: string | null;
  onSelect: (name: string) => void;
}) {
  if (checks.length === 0) {
    return (
      <p className="text-sm text-muted-foreground">
        No bot checks exist. A bot_check endpoint needs one; create it with New bot check.
      </p>
    );
  }
  return (
    <Table>
      <TableHeader>
        <TableRow>
          <TableHead>Name</TableHead>
          <TableHead>Provider</TableHead>
          <TableHead>Used by</TableHead>
        </TableRow>
      </TableHeader>
      <TableBody>
        {checks.map((c) => (
          <TableRow
            key={c.name}
            className="cursor-pointer"
            data-state={selected === c.name ? "selected" : undefined}
            onClick={() => onSelect(c.name)}
          >
            <TableCell className="font-medium">{c.name}</TableCell>
            <TableCell>
              {PROVIDER_LABELS[c.provider]}
              {!c.usable && <span className="ml-2 text-xs text-muted-foreground">(cannot be issued here)</span>}
            </TableCell>
            <TableCell className="text-muted-foreground">
              {c.used_by.length === 0 ? "No endpoint" : c.used_by.join(", ")}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

function CheckEditor({
  check,
  isNew,
  isReadOnly,
  onDone,
}: {
  check: BotCheck;
  isNew: boolean;
  isReadOnly: boolean;
  onDone: (name: string | null) => void;
}) {
  const del = useDeleteBotCheck();
  const s = useSettingsDraft<BotCheck>(check);
  const draft = s.draft ?? check;
  const [deleteError, setDeleteError] = useState<string | null>(null);
  // newSecret is the secret typed on the form, stored before the check is. It
  // is held here and never in the draft, so the key is not part of the check.
  const [newSecret, setNewSecret] = useState<NewSecret | null>(null);
  const nameOK = NAME_PATTERN.test(draft.name);
  const { save: handleSave, saving } = useSaveCheck(draft, newSecret, setNewSecret, s, onDone);

  const handleDelete = useCallback(() => {
    setDeleteError(null);
    del.mutate(check.name, {
      onSuccess: () => onDone(null),
      onError: (err) => setDeleteError(err instanceof Error ? err.message : "Failed to delete"),
    });
  }, [check.name, del, onDone]);

  const inUse = check.used_by.length > 0;
  return (
    <section aria-label={isNew ? "New bot check" : `Bot check ${check.name}`} className="space-y-5 rounded-md border p-4">
      <SaveFeedbackBanners saveError={s.saveError ?? deleteError} dirty={s.dirty} />
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h4 className="text-sm font-semibold">{isNew ? "New bot check" : check.name}</h4>
        <div className="flex items-center gap-2">
          <UpdatedByMeta updatedBy={check.updated_by} updatedAt={check.updated_at} />
          {!isReadOnly && (
            <EditorActions
              check={check}
              isNew={isNew}
              deleting={del.isPending}
              onDelete={handleDelete}
              save={
                <SaveButton
                  dirty={s.dirty && nameOK}
                  saving={saving}
                  saveSuccess={s.saveSuccess}
                  onSave={handleSave}
                />
              }
            />
          )}
        </div>
      </div>
      {isNew && (
        <Field label="Name" help="How an endpoint names the check: lowercase letters, digits, hyphens and underscores.">
          <Input
            aria-label="Name"
            className="w-72 max-w-full"
            value={draft.name}
            disabled={isReadOnly}
            onChange={(e) => s.edit({ ...draft, name: e.target.value.trim() })}
          />
        </Field>
      )}
      {inUse && (
        <p className="text-xs text-muted-foreground">
          Used by {check.used_by.join(", ")}. A change applies to every one of them on its next request.
        </p>
      )}
      <Field label="Provider" help="Proof of work needs no setup. A provider's widget is rendered by the page that calls the endpoint.">
        <select
          aria-label="Provider"
          className="h-9 w-56 rounded-md border bg-background px-2 text-sm"
          value={draft.provider}
          disabled={isReadOnly}
          onChange={(e) => s.edit({ ...draft, provider: e.target.value as BotCheckProvider })}
        >
          {PROVIDERS.map((p) => (
            <option key={p} value={p}>
              {PROVIDER_LABELS[p]}
            </option>
          ))}
        </select>
      </Field>
      {draft.provider === "pow" ? (
        <WorkFields draft={draft} onChange={s.edit} disabled={isReadOnly} />
      ) : (
        <ProviderFields
          draft={draft}
          onChange={s.edit}
          disabled={isReadOnly}
          newSecret={newSecret}
          onNewSecret={setNewSecret}
        />
      )}
    </section>
  );
}

// useSaveCheck saves the check, first storing the secret typed on the form when
// there is one (#2388): the secret is stored, then the check is saved naming
// it. A new secret is checked before anything is sent.
function useSaveCheck(
  draft: BotCheck,
  newSecret: NewSecret | null,
  setNewSecret: (next: NewSecret | null) => void,
  s: { clearError: () => void; saved: () => void; failed: (err: unknown) => void },
  onDone: (name: string | null) => void,
) {
  const put = usePutBotCheck();
  const putSecret = usePutSecret();
  const secrets = useSecrets();
  const save = useCallback(() => {
    s.clearError();
    const { name, provider, site_key, difficulty, max_difficulty, rate_threshold, challenge_ttl_seconds } = draft;
    const saveCheck = (secret_name: string) =>
      put.mutate(
        { name, input: { provider, site_key, secret_name, difficulty, max_difficulty, rate_threshold, challenge_ttl_seconds } },
        {
          onSuccess: () => {
            s.saved();
            onDone(name);
          },
          onError: s.failed,
        },
      );
    if (newSecret === null || provider === "pow") {
      saveCheck(draft.secret_name);
      return;
    }
    const found = newSecretProblems(newSecret, (secrets.data?.secrets ?? []).map((sec) => sec.name));
    if (found.length > 0) {
      s.failed(new Error(found.join(" ")));
      return;
    }
    putSecret.mutate(
      { name: newSecret.name, body: botCheckSecretInput(name, newSecret.key) },
      {
        onSuccess: (stored) => {
          setNewSecret(null);
          saveCheck(stored.name);
        },
        onError: s.failed,
      },
    );
  }, [draft, put, putSecret, secrets.data, newSecret, setNewSecret, s, onDone]);
  return { save, saving: put.isPending || putSecret.isPending };
}

// EditorActions are the editor's Delete, refused while an endpoint names the
// check, and its Save.
function EditorActions({
  check,
  isNew,
  deleting,
  onDelete,
  save,
}: {
  check: BotCheck;
  isNew: boolean;
  deleting: boolean;
  onDelete: () => void;
  save: React.ReactNode;
}) {
  const inUse = check.used_by.length > 0;
  return (
    <>
      {!isNew && (
        <Button
          size="sm"
          variant="outline"
          disabled={inUse || deleting}
          title={inUse ? `Used by ${check.used_by.join(", ")}: bind them to another check first` : undefined}
          onClick={onDelete}
        >
          Delete
        </Button>
      )}
      {save}
    </>
  );
}

function ProviderFields({
  draft,
  onChange,
  disabled,
  newSecret,
  onNewSecret,
}: {
  draft: BotCheck;
  onChange: (next: BotCheck) => void;
  disabled: boolean;
  newSecret: NewSecret | null;
  onNewSecret: (next: NewSecret | null) => void;
}) {
  const { data } = useSecrets();
  const usable = (data?.secrets ?? []).filter(isBotCheckSecret).sort(byName((sec) => sec.name));
  const choose = (value: string) => {
    if (value === NEW_SECRET) {
      onNewSecret({ name: "", key: "" });
      onChange({ ...draft, secret_name: "" });
      return;
    }
    onNewSecret(null);
    onChange({ ...draft, secret_name: value });
  };
  // A typed name or key marks the check changed, so it can be saved.
  const type = (next: NewSecret) => {
    onNewSecret(next);
    onChange({ ...draft, secret_name: next.name });
  };
  return (
    <>
      <Field label="Site key" help="The provider's site key, which the page renders the widget with. It is public.">
        <Input
          aria-label="Site key"
          className="w-96 max-w-full"
          value={draft.site_key}
          disabled={disabled}
          onChange={(e) => onChange({ ...draft, site_key: e.target.value })}
        />
      </Field>
      <Field
        label="Secret"
        help="The stored secret holding the provider's secret key, read on the server and never shown. Choose New secret to store the key here."
      >
        <select
          aria-label="Secret"
          className="h-9 w-72 max-w-full rounded-md border bg-background px-2 text-sm"
          value={newSecret ? NEW_SECRET : draft.secret_name}
          disabled={disabled}
          onChange={(e) => choose(e.target.value)}
        >
          <option value="">Choose a stored secret</option>
          {usable.map((sec) => (
            <option key={sec.name} value={sec.name}>
              {sec.name}
            </option>
          ))}
          {!newSecret && draft.secret_name && !usable.some((sec) => sec.name === draft.secret_name) && (
            <option value={draft.secret_name}>{draft.secret_name}</option>
          )}
          <option value={NEW_SECRET}>New secret</option>
        </select>
      </Field>
      {newSecret && (
        <>
          <Field
            label="Secret name"
            help={`The name the key is stored under in Admin > Secrets, limited to ${BOT_CHECK_USE} and to no persona.`}
          >
            <Input
              aria-label="Secret name"
              className="w-72 max-w-full font-mono"
              value={newSecret.name}
              disabled={disabled}
              onChange={(e) => type({ ...newSecret, name: e.target.value.trim() })}
            />
          </Field>
          <Field label="Secret key" help="The provider's secret key. It is stored encrypted and never shown again.">
            <Input
              aria-label="Secret key"
              type="password"
              autoComplete="new-password"
              className="w-96 max-w-full"
              value={newSecret.key}
              disabled={disabled}
              onChange={(e) => type({ ...newSecret, key: e.target.value })}
            />
          </Field>
        </>
      )}
    </>
  );
}

function WorkFields({
  draft,
  onChange,
  disabled,
}: {
  draft: BotCheck;
  onChange: (next: BotCheck) => void;
  disabled: boolean;
}) {
  const numbers: { key: "difficulty" | "max_difficulty" | "rate_threshold" | "challenge_ttl_seconds"; label: string; help: string }[] = [
    { key: "difficulty", label: "Difficulty", help: "Leading zero bits of the answer's hash while an endpoint is quiet. 16 is a fraction of a second in a browser." },
    { key: "max_difficulty", label: "Maximum difficulty", help: "The most a flood raises the difficulty to." },
    { key: "rate_threshold", label: "Rate threshold", help: "Answers a minute an endpoint takes before each doubling of its rate adds a bit." },
    { key: "challenge_ttl_seconds", label: "Challenge lifetime", help: "Seconds a challenge may be answered." },
  ];
  return (
    <div className="grid gap-5 sm:grid-cols-2">
      {numbers.map((n) => (
        <Field key={n.key} label={n.label} help={n.help}>
          <Input
            aria-label={n.label}
            type="number"
            className="w-28"
            value={draft[n.key]}
            disabled={disabled}
            onChange={(e) => onChange({ ...draft, [n.key]: Number(e.target.value) })}
          />
        </Field>
      ))}
    </div>
  );
}

function Field({ label, help, children }: { label: string; help: string; children: React.ReactNode }) {
  return (
    <div className="space-y-1.5">
      <Label className="text-sm">{label}</Label>
      {children}
      <p className="text-xs text-muted-foreground">{help}</p>
    </div>
  );
}
