import { useState, useEffect, useCallback, useId } from "react";
import {
  useSMTPSettings,
  useSetSMTPSettings,
  useSendTestEmail,
  useSMTPRecipientStatus,
  useSystemInfo,
  type SMTPSettings,
} from "@/api/admin/hooks";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ConfigField, ConfigSelect, ConfigToggle } from "./connections/fields";
import { ReviewQueueAlertCard } from "./ReviewQueueAlertCard";
import { SettingsCard } from "./panels";
import {
  ErrorBanner,
  ReadOnlyBanner,
  SaveButton,
  UnsavedChangesBanner,
  UpdatedByMeta,
  WarningBanner,
} from "./settingsChrome";
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

// TLS_MODE_VALUES is the set of modes the picker offers.
// A Radix Select renders nothing for a value with no matching item, so an
// unrecognised mode from the server would otherwise show an empty control.
const TLS_MODE_VALUES = new Set(TLS_MODES.map((m) => m.value));

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
        <ConfigField
          label="Password"
          help="Leave empty to keep the stored password"
          value={form.password}
          onChange={(v) => onChange({ password: v })}
          placeholder={passwordSet ? "(unchanged)" : undefined}
          sensitive
        />
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
        <ConfigSelect
          label="TLS mode"
          value={form.tls_mode}
          onChange={(v) => onChange({ tls_mode: v })}
          options={
            TLS_MODE_VALUES.has(form.tls_mode)
              ? TLS_MODES
              : [...TLS_MODES, { value: form.tls_mode, label: form.tls_mode }]
          }
        />
      </div>
    </>
  );
}

// TestEmailSection delivers a test email through the STORED settings, so it
// sits behind its own hook + local state: sending is independent of the form.
function TestEmailSection() {
  const sendTest = useSendTestEmail();
  const testFieldID = useId();
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
      <Label htmlFor={testFieldID} className="text-xs">
        Send test email
      </Label>
      <p className="mb-2 mt-1 text-xs text-muted-foreground">
        Verifies the saved configuration end to end. Save your changes first.
      </p>
      <div className="flex gap-2">
        <Input
          id={testFieldID}
          type="email"
          value={testTo}
          onChange={(e) => setTestTo(e.target.value)}
          placeholder="recipient@example.com"
          className="h-8 w-72 max-w-full text-xs"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={handleSend}
          disabled={!testTo.trim() || sendTest.isPending}
        >
          <Send />
          {sendTest.isPending ? "Sending..." : "Send test"}
        </Button>
      </div>
      {showOptOutNotice && (
        <Alert variant="warning" className="mt-2 px-3 py-2">
          <AlertCircle />
          <AlertDescription className="text-xs">
            This address has opted out of notification emails; the test will still
            send.
          </AlertDescription>
        </Alert>
      )}
      {result && (
        <Alert
          variant={result.ok ? "success" : "destructive"}
          className="mt-2 px-3 py-2"
        >
          {result.ok ? <Check /> : <XCircle />}
          <AlertDescription className="text-xs">{result.message}</AlertDescription>
        </Alert>
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
    <SettingsCard
      icon={Mail}
      title="Email (SMTP)"
      description="Outbound mail server used for email notifications"
      notices={
        <StatusBanners
          isReadOnly={isReadOnly}
          loadFailed={!!loadError}
          warnings={settings?.warnings ?? []}
          noDelivery={hasNoDeliveryPath(settings)}
          onRetry={() => void refetch()}
        />
      }
      feedback={<SaveFeedbackBanners saveError={saveError} dirty={dirty} />}
      action={
        <>
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
        </>
      }
    >
      {isLoading ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <div className="space-y-4">
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
    </SettingsCard>
  );
}
