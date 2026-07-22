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
import { cn } from "@/lib/utils";
import {
  Save,
  Check,
  AlertCircle,
  RefreshCw,
  XCircle,
  Send,
  Mail,
} from "lucide-react";

// ---------------------------------------------------------------------------
// Shared error banner
// ---------------------------------------------------------------------------

function ErrorBanner({ message, onRetry }: { message: string; onRetry?: () => void }) {
  return (
    <div className="flex items-center gap-2 border-b bg-red-50 px-5 py-2.5 text-xs text-red-700 dark:bg-red-950/30 dark:text-red-400">
      <XCircle className="h-3.5 w-3.5 shrink-0" />
      <span className="flex-1">{message}</span>
      {onRetry && (
        <button
          type="button"
          onClick={onRetry}
          className="inline-flex items-center gap-1 rounded px-2 py-1 text-xs font-medium hover:bg-red-100 dark:hover:bg-red-900/30"
        >
          <RefreshCw className="h-3 w-3" />
          Retry
        </button>
      )}
    </div>
  );
}

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

function SaveButton({
  dirty,
  saving,
  saveSuccess,
  onSave,
}: {
  dirty: boolean;
  saving: boolean;
  saveSuccess: boolean;
  onSave: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onSave}
      disabled={!dirty || saving}
      className={cn(
        "inline-flex items-center gap-1.5 rounded-md px-3 py-1.5 text-xs font-medium transition-all disabled:opacity-50",
        saveSuccess
          ? "bg-green-600 text-white"
          : "bg-primary text-primary-foreground hover:bg-primary/90",
      )}
    >
      {saveSuccess ? (
        <>
          <Check className="h-3 w-3" />
          Saved
        </>
      ) : saving ? (
        "Saving..."
      ) : (
        <>
          <Save className="h-3 w-3" />
          Save
        </>
      )}
    </button>
  );
}

function UpdatedByMeta({ settings }: { settings?: SMTPSettings }) {
  if (!settings?.updated_by) return null;
  return (
    <span className="text-xs text-muted-foreground">
      Updated by {settings.updated_by}
      {settings.updated_at &&
        ` · ${new Date(settings.updated_at).toLocaleDateString()}`}
    </span>
  );
}

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

function StatusBanners({
  isReadOnly,
  loadFailed,
  onRetry,
}: {
  isReadOnly: boolean;
  loadFailed: boolean;
  onRetry: () => void;
}) {
  return (
    <>
      {isReadOnly && (
        <div className="flex items-center gap-2 border-b bg-amber-50/50 px-5 py-2 text-xs text-amber-700 dark:bg-amber-950/20 dark:text-amber-400">
          <AlertCircle className="h-3.5 w-3.5" />
          Configuration is read-only: no database configured. Set <code className="font-mono">database.dsn</code> to enable editing.
        </div>
      )}
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
      {dirty && !saveError && (
        <div className="flex items-center gap-2 border-b bg-amber-50/50 px-5 py-1.5 text-[11px] text-amber-700 dark:bg-amber-950/20 dark:text-amber-400">
          <AlertCircle className="h-3 w-3" />
          You have unsaved changes
        </div>
      )}
    </>
  );
}

// ---------------------------------------------------------------------------
// AdminSettingsPage: platform settings (/admin/settings). First (and so far
// only) section: Email (SMTP) delivery used by the notification mailer (#631).
// ---------------------------------------------------------------------------

export function AdminSettingsPage() {
  const { data: systemInfo } = useSystemInfo();
  const isReadOnly = systemInfo?.config_mode === "file";
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
          <UpdatedByMeta settings={settings} />
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
