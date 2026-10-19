import { useCallback } from "react";
import { ShieldCheck } from "lucide-react";
import { useBotCheckSettings, useSecrets, useSetBotCheckSettings } from "@/api/admin/hooks";
import type { BotCheckProvider, BotCheckSettings } from "@/api/admin/types";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SettingsCard } from "./panels";
import { SaveButton, SaveFeedbackBanners, UpdatedByMeta } from "./settingsChrome";
import { useSettingsDraft } from "./useSettingsDraft";

// BotCheckSettingsCard is the bot check a public endpoint requires (#2173):
// the platform's own proof of work by default, Cloudflare Turnstile or
// hCaptcha with the stored secret its key is kept in, or none, which refuses
// public endpoints until the override is set.

const PROVIDERS: { id: BotCheckProvider; label: string }[] = [
  { id: "pow", label: "Proof of work" },
  { id: "turnstile", label: "Cloudflare Turnstile" },
  { id: "hcaptcha", label: "hCaptcha" },
  { id: "none", label: "None" },
];

/** SECRET_USE is what a provider's stored secret lists in allow_connections. */
const SECRET_USE = "bot_check";

export function BotCheckSettingsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data, isLoading } = useBotCheckSettings();
  const save = useSetBotCheckSettings();
  const s = useSettingsDraft<BotCheckSettings>(data);
  const draft = s.draft;

  const handleSave = useCallback(() => {
    if (!draft) return;
    s.clearError();
    const { updated_by: _by, updated_at: _at, key_available: _key, public_allowed: _public, ...input } = draft;
    save.mutate(input, { onSuccess: s.saved, onError: s.failed });
  }, [draft, save, s]);

  return (
    <SettingsCard
      icon={ShieldCheck}
      title="Bot check"
      description="What a request to a public endpoint must pass. Proof of work is the platform's own and calls no third party; Turnstile and hCaptcha are verified on the server with a key kept as a stored secret."
      feedback={<SaveFeedbackBanners saveError={s.saveError} dirty={s.dirty} />}
      action={
        <>
          <UpdatedByMeta updatedBy={data?.updated_by} updatedAt={data?.updated_at} />
          {!isReadOnly && <SaveButton dirty={s.dirty} saving={save.isPending} saveSuccess={s.saveSuccess} onSave={handleSave} />}
        </>
      }
    >
      {isLoading || !draft ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="space-y-5">
          <Notices settings={data ?? draft} />
          <Field label="Provider" help="Proof of work needs no setup. A provider's widget is rendered by the page that calls the endpoint.">
            <select
              aria-label="Provider"
              className="h-9 w-56 rounded-md border bg-background px-2 text-sm"
              value={draft.provider}
              disabled={isReadOnly}
              onChange={(e) => s.edit({ ...draft, provider: e.target.value as BotCheckProvider })}
            >
              {PROVIDERS.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.label}
                </option>
              ))}
            </select>
          </Field>
          {(draft.provider === "turnstile" || draft.provider === "hcaptcha") && (
            <ProviderFields draft={draft} onChange={s.edit} disabled={isReadOnly} />
          )}
          {draft.provider === "pow" && <WorkFields draft={draft} onChange={s.edit} disabled={isReadOnly} />}
          <label className="flex items-start gap-2 text-sm">
            <input
              type="checkbox"
              className="mt-0.5"
              checked={draft.allow_public_without_check}
              disabled={isReadOnly}
              onChange={(e) => s.edit({ ...draft, allow_public_without_check: e.target.checked })}
            />
            <span>
              Allow public endpoints without a bot check
              <span className="block text-xs text-muted-foreground">
                With the provider None, a public endpoint is refused, and cannot be bound, until this is set.
              </span>
            </span>
          </label>
        </div>
      )}
    </SettingsCard>
  );
}

function Notices({ settings }: { settings: BotCheckSettings }) {
  if (settings.provider === "pow" && !settings.key_available) {
    return (
      <Alert>
        <AlertDescription>
          This deployment has no browser-session signing key (auth.browser_session.signing_key), so it cannot sign a
          proof-of-work challenge. Choose a provider, or set the key.
        </AlertDescription>
      </Alert>
    );
  }
  if (!settings.public_allowed) {
    return (
      <Alert>
        <AlertDescription>Public endpoints are refused under these settings.</AlertDescription>
      </Alert>
    );
  }
  return null;
}

function ProviderFields({
  draft,
  onChange,
  disabled,
}: {
  draft: BotCheckSettings;
  onChange: (next: BotCheckSettings) => void;
  disabled: boolean;
}) {
  const { data } = useSecrets();
  const usable = (data?.secrets ?? []).filter(
    (sec) => sec.kind === "value" && sec.allow_connections.includes(SECRET_USE) && sec.allow_personas.length === 0,
  );
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
        help={`The stored secret holding the provider's secret key (Admin > Secrets). It lists ${SECRET_USE} in its connections and no personas; the key is read on the server and never shown.`}
      >
        <select
          aria-label="Secret"
          className="h-9 w-72 max-w-full rounded-md border bg-background px-2 text-sm"
          value={draft.secret_name}
          disabled={disabled}
          onChange={(e) => onChange({ ...draft, secret_name: e.target.value })}
        >
          <option value="">Choose a stored secret</option>
          {usable.map((sec) => (
            <option key={sec.name} value={sec.name}>
              {sec.name}
            </option>
          ))}
          {draft.secret_name && !usable.some((sec) => sec.name === draft.secret_name) && (
            <option value={draft.secret_name}>{draft.secret_name}</option>
          )}
        </select>
      </Field>
    </>
  );
}

function WorkFields({
  draft,
  onChange,
  disabled,
}: {
  draft: BotCheckSettings;
  onChange: (next: BotCheckSettings) => void;
  disabled: boolean;
}) {
  const numbers: { key: keyof BotCheckSettings; label: string; help: string }[] = [
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
            value={draft[n.key] as number}
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
