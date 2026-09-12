import { useState, useEffect, useCallback } from "react";
import { PlugZap } from "lucide-react";
import {
  useConnectionAlert,
  useSetConnectionAlert,
  type ConnectionAlertSettings,
} from "@/api/admin/hooks";
import { ConfigField, ConfigToggle } from "./connections/fields";
import { SettingsCard } from "./panels";
import { RecipientsEditor } from "./RecipientsEditor";
import {
  AlertStatusBanners,
  SaveButton,
  SaveFeedbackBanners,
  UpdatedByMeta,
} from "./settingsChrome";

// The connection-revocation alert settings section (#1694).
//
// What it configures is who hears that a connection stopped working. The
// person who authorized it is told as soon as the platform discards the
// credential and needs no configuration; this section is the escalation, for
// the case the whole alert exists for — that person is unreachable while
// everyone using the connection, and every schedule that runs through it, is
// stopped.

// FormState mirrors the PUT body with the window kept as a string while
// typing, so the field can be emptied without snapping back to 0.
interface FormState {
  enabled: boolean;
  escalate_after_hours: string;
  recipients: string[];
}

const DEFAULT_FORM: FormState = {
  enabled: true,
  escalate_after_hours: "24",
  recipients: [],
};

function formFrom(settings: ConnectionAlertSettings): FormState {
  return {
    enabled: settings.enabled,
    escalate_after_hours: String(settings.escalate_after_hours || 24),
    recipients: settings.recipients ?? [],
  };
}

export function ConnectionAlertCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data: settings, isLoading, error: loadError, refetch } = useConnectionAlert();
  const save = useSetConnectionAlert();

  const [form, setForm] = useState<FormState>(DEFAULT_FORM);
  const [dirty, setDirty] = useState(false);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (!settings) return;
    setForm(formFrom(settings));
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
    save.mutate(
      {
        enabled: form.enabled,
        escalate_after_hours: parseInt(form.escalate_after_hours, 10) || 24,
        recipients: form.recipients,
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
  }, [form, save]);

  return (
    <SettingsCard
      icon={PlugZap}
      title="Connection revocation alerts"
      description="When an upstream rejects a connection's refresh, the platform discards the credential and every call through that connection fails until someone reauthorizes it. This is who hears about it."
      notices={
        <AlertStatusBanners
          warnings={settings?.warnings ?? []}
          isReadOnly={isReadOnly}
          loadFailed={!!loadError}
          onRetry={() => void refetch()}
        />
      }
      feedback={<SaveFeedbackBanners saveError={saveError} dirty={dirty} />}
      action={
        <>
          <UpdatedByMeta updatedBy={settings?.updated_by} updatedAt={settings?.updated_at} />
          {!isReadOnly && (
            <SaveButton
              dirty={dirty}
              saving={save.isPending}
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
          <ConfigToggle
            label="Enabled"
            help="Email the person who authorized a connection as soon as its credential is discarded. Off, the revocation is still recorded in the connection's auth history and shown on its status card, and nobody is told."
            checked={form.enabled}
            onChange={(v) => handleChange({ enabled: v })}
          />
          <div className="grid gap-4 sm:grid-cols-2">
            <ConfigField
              label="Escalate after (hours)"
              type="number"
              help="How long a connection stays revoked before the addresses below are told. Reauthorizing it at any point cancels the escalation."
              value={form.escalate_after_hours}
              onChange={(v) => handleChange({ escalate_after_hours: v })}
              placeholder="24"
              mono
            />
          </div>
          <RecipientsEditor
            recipients={form.recipients}
            onChange={(recipients) => handleChange({ recipients })}
            label="Escalation recipients"
            placeholder="platform-admin@example.com"
            help="Who to tell when a revoked connection has not been reauthorized. Leave it empty and only the person who authorized the connection is told. Each recipient's own notification preferences still apply."
          />
        </div>
      )}
    </SettingsCard>
  );
}
