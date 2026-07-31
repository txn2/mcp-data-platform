import { useState, useEffect, useCallback } from "react";
import {
  useSMTPSettings,
  useSetSMTPSettings,
  useSendTestEmail,
  useSMTPRecipientStatus,
  useSystemInfo,
  type SMTPSettings,
} from "@/api/admin/hooks";
import { ConfigField, ConfigToggle } from "./connections/fields";
import { ReviewQueueAlertCard } from "./ReviewQueueAlertCard";
import {
  ErrorBanner,
  ReadOnlyBanner,
  SaveButton,
  UnsavedChangesBanner,
  UpdatedByMeta,
  WarningBanner,
} from "./settingsChrome";
import { cn } from "@/lib/utils";
import { AlertCircle, Check, XCircle, Send, Mail } from "lucide-react";

// ---------------------------------------------------------------------------
// SMTP form state
// ---------------------------------------------------------------------------

// TLS_MODES are the connection-security options the mailer supports.
const TLS_MODES = [
  { value: "starttls", label: "STARTTLS" },
  { value: "implicit", label: "Implicit TLS" },
  { value: "none", label: "None" },
];

// FormState mirrors the PUT body with text inputs kept as strings; port is
// parsed on save. The password field is write-only: it is never populated
// from the server, and an empty value keeps the stored password.
interface FormState {
  enabled: boolean;
  host: string;
  port: string;
  username: string;
  password: string;
  from: string;
  from_name: string;
  tls_mode: string;
}

const DEFAULT_FORM: FormState = {
  enabled: false,
  host: "",
  port: "587",
  username: "",
  password: "",
  from: "",
  from_name: "",
  tls_mode: "starttls",
};

// ---------------------------------------------------------------------------
// Sub-sections
// ---------------------------------------------------------------------------

function SMTPFields({
  form,
  passwordSet,
  onChange,
}: {
  form: FormState;
  passwordSet: boolean;
  onChange: (patch: Partial<FormState>) => void;
}) {
  return (
    <>
      <ConfigToggle
        label="Enabled"
        help="Deliver email notifications through this SMTP server"
        checked={form.enabled}
        onChange={(v) => onChange({ enabled: v })}
      />

      <div className="grid gap-4 sm:grid-cols-2">
        <ConfigField
          label="Host"
          help="SMTP server hostname"
          value={form.host}
          onChange={(v) => onChange({ host: v })}
          placeholder="smtp.example.com"
          mono
        />
        <ConfigField
          label="Port"
          type="number"
          value={form.port}
          onChange={(v) => onChange({ port: v })}
          placeholder="587"
          mono
        />
        <ConfigField
          label="Username"
          value={form.username}
          onChange={(v) => onChange({ username: v })}
          placeholder="mailer@example.com"
          mono
        />
        <div>
          <label className="mb-1 block text-xs font-medium">Password</label>
          <input
            type="password"
            value={form.password}
            onChange={(e) => onChange({ password: e.target.value })}
            placeholder={passwordSet ? "(unchanged)" : undefined}
            autoComplete="off"
            className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          />
          <p className="mt-1 text-xs text-muted-foreground">
            Leave empty to keep the stored password
          </p>
        </div>
        <ConfigField
          label="From address"
          value={form.from}
          onChange={(v) => onChange({ from: v })}
          placeholder="platform@example.com"
          mono
        />
        <ConfigField
          label="From name"
          value={form.from_name}
          onChange={(v) => onChange({ from_name: v })}
          placeholder="Data Platform"
        />
        <div>
          <label className="mb-1 block text-xs font-medium">TLS mode</label>
          <select
            value={form.tls_mode}
            onChange={(e) => onChange({ tls_mode: e.target.value })}
            className="w-full rounded-md border bg-background px-3 py-2 text-sm outline-none ring-ring focus:ring-2"
          >
            {TLS_MODES.map((m) => (
              <option key={m.value} value={m.value}>
                {m.label}
              </option>
            ))}
          </select>
        </div>
      </div>
    </>
  );
}

// TestEmailSection delivers a test email through the STORED settings, so it
// sits behind its own hook + local state: sending is independent of the form.
function TestEmailSection() {
  const sendTest = useSendTestEmail();
  const [testTo, setTestTo] = useState("");
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);

  // Opt-out notice (#1022): the test deliberately bypasses preference gating,
  // so surface the target's opt-out state as information, never as a block.
  // Debounced so the status query fires once per pause, not per keystroke.
  const [debouncedTo, setDebouncedTo] = useState("");
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedTo(testTo.trim()), 400);
    return () => clearTimeout(timer);
  }, [testTo]);
  const { data: recipientStatus } = useSMTPRecipientStatus(debouncedTo);
  const showOptOutNotice =
    recipientStatus?.opted_out && recipientStatus.to === debouncedTo.toLowerCase();

  const handleSend = useCallback(() => {
    setResult(null);
    sendTest.mutate(testTo.trim(), {
      onSuccess: (res) => {
        setResult({ ok: true, message: `Test email sent to ${res.to}` });
      },
      onError: (err) => {
        setResult({
          ok: false,
          message: err instanceof Error ? err.message : "Failed to send test email",
        });
      },
    });
  }, [sendTest, testTo]);

  return (
    <div className="border-t pt-4">
      <label className="mb-1 block text-xs font-medium">Send test email</label>
      <p className="mb-2 text-xs text-muted-foreground">
        Verifies the saved configuration end to end. Save your changes first.
      </p>
      <div className="flex gap-2">
        <input
          type="email"
          value={testTo}
          onChange={(e) => setTestTo(e.target.value)}
          placeholder="recipient@example.com"
          className="w-72 max-w-full rounded-md border bg-background px-3 py-1.5 text-xs outline-none ring-ring focus:ring-2"
        />
        <button
          type="button"
          onClick={handleSend}
          disabled={!testTo.trim() || sendTest.isPending}
          className="inline-flex items-center gap-1.5 rounded-md border px-3 py-1.5 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-50"
        >
          <Send className="h-3 w-3" />
          {sendTest.isPending ? "Sending..." : "Send test"}
        </button>
      </div>
      {showOptOutNotice && (
        <p className="mt-2 flex items-center gap-1.5 text-xs text-amber-700 dark:text-amber-400">
          <AlertCircle className="h-3.5 w-3.5 shrink-0" />
          This address has opted out of notification emails; the test will still send.
        </p>
      )}
      {result && (
        <p
          className={cn(
            "mt-2 flex items-center gap-1.5 text-xs",
            result.ok
              ? "text-green-600 dark:text-green-500"
              : "text-red-700 dark:text-red-400",
          )}
        >
          {result.ok ? (
            <Check className="h-3.5 w-3.5 shrink-0" />
          ) : (
            <XCircle className="h-3.5 w-3.5 shrink-0" />
          )}
          {result.message}
        </p>
      )}
    </div>
  );
}

// hasNoDeliveryPath reports whether the STORED settings leave the platform
// unable to send: never written, disabled, or missing a host. Unloaded
// settings report false -- the state is unknown, not known-bad.
function hasNoDeliveryPath(settings?: SMTPSettings): boolean {
  if (!settings) return false;
  return !settings.enabled || !settings.host;
}

function StatusBanners({
  isReadOnly,
  loadFailed,
  warnings,
  noDelivery,
  onRetry,
}: {
  isReadOnly: boolean;
  loadFailed: boolean;
  warnings: string[];
  noDelivery: boolean;
  onRetry: () => void;
}) {
  return (
    <>
      {/* The consequence of leaving this section unset or disabled (#1099):
          triggers keep queueing, so the effect is silent expiry rather than
          nothing happening at all. */}
      {noDelivery && (
        <WarningBanner>
          No delivery path is configured, so no notification email can be sent. Share
          and comment activity still queues notifications, and those queued rows expire
          undelivered after 7 days. Users see their notification preferences as inert
          until this section is enabled with a host.
        </WarningBanner>
      )}
      {/* Hazards in the SAVED configuration (#1072), not validation of the
          current form: the server evaluates them against the stored settings,
          where an unchanged password is still a credential on the wire. */}
      {warnings.map((warning) => (
        <WarningBanner key={warning}>{warning}</WarningBanner>
      ))}
      {isReadOnly && <ReadOnlyBanner />}
      {loadFailed && (
        <ErrorBanner
          message="Failed to load SMTP settings. The server may be unavailable."
          onRetry={onRetry}
        />
      )}
    </>
  );
}

function SaveFeedbackBanners({
  saveError,
  dirty,
}: {
  saveError: string | null;
  dirty: boolean;
}) {
  return (
    <>
      {saveError && <ErrorBanner message={saveError} />}
      {dirty && !saveError && <UnsavedChangesBanner />}
    </>
  );
}

// ---------------------------------------------------------------------------
// AdminSettingsPage: platform settings (/admin/settings). Two sections today:
// Email (SMTP) delivery used by the notification mailer (#631), and the
// knowledge review-queue staleness alert that sends through it (#803).
// ---------------------------------------------------------------------------

export function AdminSettingsPage() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
  return (
    <div className="space-y-6">
      <SMTPCard isReadOnly={isReadOnly} />
      <ReviewQueueAlertCard isReadOnly={isReadOnly} />
    </div>
  );
}

// SMTPCard is the outbound mail server section (#631).
function SMTPCard({ isReadOnly }: { isReadOnly: boolean }) {
  const {
    data: settings,
    isLoading,
    error: loadError,
    refetch,
  } = useSMTPSettings();
  const setSettings = useSetSMTPSettings();

  const [form, setForm] = useState<FormState>(DEFAULT_FORM);
  const [dirty, setDirty] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  // Sync from server. The password never round-trips: the field resets to
  // empty and password_set drives the "(unchanged)" placeholder.
  useEffect(() => {
    if (!settings) return;
    setForm({
      enabled: settings.enabled,
      host: settings.host,
      port: String(settings.port || 587),
      username: settings.username,
      password: "",
      from: settings.from,
      from_name: settings.from_name,
      tls_mode: settings.tls_mode || "starttls",
    });
    setDirty(false);
  }, [settings]);

  const handleChange = useCallback((patch: Partial<FormState>) => {
    setForm((prev) => ({ ...prev, ...patch }));
    setDirty(true);
    setSaveSuccess(false);
    setSaveError(null);
  }, []);

  const handleSave = useCallback(() => {
    setSaveError(null);
    setSettings.mutate(
      {
        enabled: form.enabled,
        host: form.host.trim(),
        port: parseInt(form.port, 10) || 587,
        username: form.username.trim(),
        password: form.password,
        from: form.from.trim(),
        from_name: form.from_name.trim(),
        tls_mode: form.tls_mode,
      },
      {
        onSuccess: () => {
          setDirty(false);
          setSaveSuccess(true);
          setTimeout(() => setSaveSuccess(false), 2500);
        },
        onError: (err) => {
          setSaveError(err instanceof Error ? err.message : "Failed to save");
        },
      },
    );
  }, [form, setSettings]);

  return (
    <div className="overflow-hidden rounded-lg border bg-card">
      <StatusBanners
        isReadOnly={isReadOnly}
        loadFailed={!!loadError}
        warnings={settings?.warnings ?? []}
        noDelivery={hasNoDeliveryPath(settings)}
        onRetry={() => void refetch()}
      />

      {/* Header bar */}
      <div className="flex items-center justify-between border-b px-5 py-3">
        <div className="flex items-center gap-3">
          <Mail className="h-4 w-4 text-muted-foreground" />
          <div>
            <h3 className="text-sm font-semibold leading-none">Email (SMTP)</h3>
            <p className="mt-1 text-xs text-muted-foreground">
              Outbound mail server used for email notifications
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <UpdatedByMeta
            updatedBy={settings?.updated_by}
            updatedAt={settings?.updated_at}
          />
          {!isReadOnly && (
            <SaveButton
              dirty={dirty}
              saving={setSettings.isPending}
              saveSuccess={saveSuccess}
              onSave={handleSave}
            />
          )}
        </div>
      </div>

      <SaveFeedbackBanners saveError={saveError} dirty={dirty} />

      {isLoading ? (
        <div className="p-5 text-sm text-muted-foreground">Loading...</div>
      ) : (
        <div className="space-y-4 p-5">
          <SMTPFields
            form={form}
            passwordSet={settings?.password_set ?? false}
            onChange={handleChange}
          />
          {/* Send-test is hidden in read-only mode alongside Save: with no
              database there is no stored configuration to exercise. */}
          {!isReadOnly && <TestEmailSection />}
        </div>
      )}
    </div>
  );
}
