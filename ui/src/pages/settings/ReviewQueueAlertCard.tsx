import { useState, useEffect, useCallback } from "react";
import { BellRing, Plus, X } from "lucide-react";
import {
  useReviewQueueAlert,
  useSetReviewQueueAlert,
  type ReviewQueueAlertSettings,
} from "@/api/admin/hooks";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { ConfigField, ConfigToggle } from "./connections/fields";
import { SettingsCard } from "./panels";
import {
  ErrorBanner,
  ReadOnlyBanner,
  SaveButton,
  UnsavedChangesBanner,
  UpdatedByMeta,
  WarningBanner,
} from "./settingsChrome";

// The knowledge review-queue staleness alert (#803). #764 made review debt
// visible to anyone who opens the Insights tab; this section is the push
// signal for everyone who does not, so the settings it exposes are the ones
// that decide whether an email is ever sent: the thresholds, who hears about
// it, and how often.

// FormState mirrors the PUT body with the numbers kept as strings while typing,
// so a field can be emptied without snapping back to 0.
interface FormState {
  enabled: boolean;
  pending_threshold: string;
  oldest_pending_days: string;
  cooldown_hours: string;
  recipients: string[];
}

const DEFAULT_FORM: FormState = {
  enabled: true,
  pending_threshold: "0",
  oldest_pending_days: "30",
  cooldown_hours: "24",
  recipients: [],
};

function formFrom(settings: ReviewQueueAlertSettings): FormState {
  return {
    enabled: settings.enabled,
    pending_threshold: String(settings.pending_threshold ?? 0),
    oldest_pending_days: String(settings.oldest_pending_days ?? 0),
    cooldown_hours: String(settings.cooldown_hours || 24),
    recipients: settings.recipients ?? [],
  };
}

// RecipientsEditor edits the alert's distribution list. The list is the whole
// audience for this alert, so it is edited explicitly rather than inferred
// from roles: the platform has no queryable set of admins.
function RecipientsEditor({
  recipients,
  onChange,
}: {
  recipients: string[];
  onChange: (next: string[]) => void;
}) {
  const [draft, setDraft] = useState("");

  const add = useCallback(() => {
    const value = draft.trim();
    if (!value || recipients.includes(value)) {
      setDraft("");
      return;
    }
    onChange([...recipients, value]);
    setDraft("");
  }, [draft, recipients, onChange]);

  return (
    <div className="space-y-1.5">
      {/* The heading names the list; the input's own aria-label names what
          typing in it does. Wiring the two together would replace "Add
          recipient" with "Recipients" on the control that adds one. */}
      <Label className="text-xs">Recipients</Label>
      <div className="flex gap-2">
        <Input
          type="email"
          value={draft}
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add();
            }
          }}
          // Commit on blur as well, so an address typed but not added is not
          // silently dropped by the Save the operator reaches for next.
          onBlur={add}
          placeholder="data-admin@example.com"
          aria-label="Add recipient"
          className="font-mono"
        />
        <Button type="button" variant="outline" onClick={add} disabled={!draft.trim()}>
          <Plus />
          Add
        </Button>
      </div>
      {recipients.length > 0 && (
        <ul className="flex flex-wrap gap-1.5 pt-1">
          {recipients.map((email) => (
            <li key={email}>
              <Badge variant="outline" className="gap-1 bg-muted/40 py-1 pl-2.5 pr-1 font-mono">
                {email}
                <Button
                  type="button"
                  variant="ghost"
                  size="icon-xs"
                  onClick={() => onChange(recipients.filter((r) => r !== email))}
                  aria-label={`Remove ${email}`}
                  className="size-4 rounded-full"
                >
                  <X />
                </Button>
              </Badge>
            </li>
          ))}
        </ul>
      )}
      <p className="text-xs text-muted-foreground">
        Each recipient's own notification preferences still apply: someone who turned
        email notifications off receives nothing.
      </p>
    </div>
  );
}

function ThresholdFields({
  form,
  onChange,
}: {
  form: FormState;
  onChange: (patch: Partial<FormState>) => void;
}) {
  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <ConfigField
        label="Pending count threshold"
        type="number"
        help="Alert once this many insights are awaiting review. 0 turns this condition off."
        value={form.pending_threshold}
        onChange={(v) => onChange({ pending_threshold: v })}
        placeholder="0"
        mono
      />
      <ConfigField
        label="Oldest pending age (days)"
        type="number"
        help="Alert once the oldest pending insight reaches this age. 0 turns this condition off."
        value={form.oldest_pending_days}
        onChange={(v) => onChange({ oldest_pending_days: v })}
        placeholder="30"
        mono
      />
      <ConfigField
        label="Re-alert cooldown (hours)"
        type="number"
        help="Minimum gap between alerts while the queue stays over threshold. A queue worked back under and crossing again alerts immediately."
        value={form.cooldown_hours}
        onChange={(v) => onChange({ cooldown_hours: v })}
        placeholder="24"
        mono
      />
    </div>
  );
}

// StatusBanners states why the section cannot act, before the form that
// configures it: warnings the server raised against the SAVED configuration,
// file-config mode, and a failed load.
function StatusBanners({
  warnings,
  isReadOnly,
  loadFailed,
  onRetry,
}: {
  warnings: string[];
  isReadOnly: boolean;
  loadFailed: boolean;
  onRetry: () => void;
}) {
  return (
    <>
      {warnings.map((warning) => (
        <WarningBanner key={warning}>{warning}</WarningBanner>
      ))}
      {isReadOnly && <ReadOnlyBanner />}
      {loadFailed && (
        <ErrorBanner
          message="Failed to load review-queue alert settings. The server may be unavailable."
          onRetry={onRetry}
        />
      )}
    </>
  );
}

// SaveFeedbackBanners reports the outcome of the last save attempt and the
// standing unsaved-changes state.
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

export function ReviewQueueAlertCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data: settings, isLoading, error: loadError, refetch } = useReviewQueueAlert();
  const save = useSetReviewQueueAlert();

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
        pending_threshold: parseInt(form.pending_threshold, 10) || 0,
        oldest_pending_days: parseInt(form.oldest_pending_days, 10) || 0,
        cooldown_hours: parseInt(form.cooldown_hours, 10) || 24,
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
      icon={BellRing}
      title="Review queue alert"
      description="Email a digest when knowledge insights are left unreviewed"
      // Warnings come from the SAVED configuration: an enabled alert with no
      // recipients or no threshold saves cleanly and delivers nothing.
      notices={
        <StatusBanners
          warnings={settings?.warnings ?? []}
          isReadOnly={isReadOnly}
          loadFailed={!!loadError}
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
            help="Check the pending review queue hourly and alert when it crosses a threshold"
            checked={form.enabled}
            onChange={(v) => handleChange({ enabled: v })}
          />
          <ThresholdFields form={form} onChange={handleChange} />
          <RecipientsEditor
            recipients={form.recipients}
            onChange={(recipients) => handleChange({ recipients })}
          />
        </div>
      )}
    </SettingsCard>
  );
}
