import { useCallback, useEffect, useState } from "react";
import { MessageSquareWarning } from "lucide-react";
import {
  usePersonas,
  usePlatformFeedbackSettings,
  useSetPlatformFeedbackSettings,
  type PlatformFeedbackSettings,
  type PlatformFeedbackSettingsView,
} from "@/api/admin/hooks";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useUnsavedChangesGuard } from "@/lib/navigationGuard";
import { SettingsCard } from "./panels";
import { ChannelsPicker } from "./ChannelsPicker";
import {
  ErrorBanner,
  ReadOnlyBanner,
  SaveButton,
  SaveFeedbackBanners,
  UpdatedByMeta,
  WarningBanner,
} from "./settingsChrome";

// PlatformFeedbackCard is whether agents may report what the platform could
// not do for them, where a post is sent, who may post and how often (#2820).

const DEFAULTS: PlatformFeedbackSettings = {
  enabled: false,
  channels: [],
  personas: [],
  session_limit: 5,
  persona_daily_limit: 50,
};

function formFrom(v: PlatformFeedbackSettingsView): PlatformFeedbackSettings {
  return {
    enabled: v.enabled,
    channels: v.channels ?? [],
    personas: v.personas ?? [],
    session_limit: v.session_limit || DEFAULTS.session_limit,
    persona_daily_limit: v.persona_daily_limit || DEFAULTS.persona_daily_limit,
  };
}

function useDraft(view: PlatformFeedbackSettingsView | undefined) {
  const save = useSetPlatformFeedbackSettings();
  const [form, setForm] = useState<PlatformFeedbackSettings>(DEFAULTS);
  const [dirty, setDirty] = useState(false);
  useUnsavedChangesGuard(dirty);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (!view) return;
    setForm(formFrom(view));
    setDirty(false);
  }, [view]);

  const edit = useCallback((patch: Partial<PlatformFeedbackSettings>) => {
    setForm((prev) => ({ ...prev, ...patch }));
    setDirty(true);
    setSaveSuccess(false);
    setSaveError(null);
  }, []);

  const handleSave = useCallback(() => {
    setSaveError(null);
    save.mutate(form, {
      onSuccess: () => {
        setDirty(false);
        setSaveSuccess(true);
        setTimeout(() => setSaveSuccess(false), 2500);
      },
      onError: (err) => setSaveError(err instanceof Error ? err.message : "Failed to save"),
    });
  }, [form, save]);

  return { form, dirty, saveSuccess, saveError, saving: save.isPending, edit, handleSave };
}

export function PlatformFeedbackCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data: view, isLoading, error, refetch } = usePlatformFeedbackSettings();
  const d = useDraft(view);
  return (
    <SettingsCard
      icon={MessageSquareWarning}
      title="Platform feedback"
      description="What agents report the platform could not do for them: a tool that refused twice, or a parameter, tool or capability that does not exist. Posts go to the channels chosen here, for whoever runs the platform."
      notices={<Notices view={view} isReadOnly={isReadOnly} loadFailed={!!error} onRetry={() => void refetch()} />}
      feedback={<SaveFeedbackBanners saveError={d.saveError} dirty={d.dirty} />}
      action={
        <>
          <UpdatedByMeta updatedBy={view?.updated_by} updatedAt={view?.updated_at} />
          {!isReadOnly && (
            <SaveButton dirty={d.dirty} saving={d.saving} saveSuccess={d.saveSuccess} onSave={d.handleSave} />
          )}
        </>
      }
    >
      {isLoading || !view ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <PlatformFeedbackForm form={d.form} isReadOnly={isReadOnly} onEdit={d.edit} />
      )}
    </SettingsCard>
  );
}

// Notices says why the section is not doing its job: on with nowhere to send,
// read-only, or not loaded.
function Notices({
  view,
  isReadOnly,
  loadFailed,
  onRetry,
}: {
  view?: PlatformFeedbackSettingsView;
  isReadOnly: boolean;
  loadFailed: boolean;
  onRetry: () => void;
}) {
  const nowhere = !!view?.enabled && (view.channels ?? []).length === 0;
  return (
    <>
      {nowhere && (
        <WarningBanner>
          <strong>On, with no channel chosen.</strong> Posts are kept on the Platform feedback page and sent nowhere.
        </WarningBanner>
      )}
      {isReadOnly && <ReadOnlyBanner />}
      {loadFailed && <ErrorBanner message="Failed to load the platform feedback settings." onRetry={onRetry} />}
    </>
  );
}

export function PlatformFeedbackForm({
  form,
  isReadOnly,
  onEdit: edit,
}: {
  form: PlatformFeedbackSettings;
  isReadOnly: boolean;
  onEdit: (patch: Partial<PlatformFeedbackSettings>) => void;
}) {
  const personas = usePersonas().data?.personas ?? [];
  const only = form.personas.length > 0;
  const togglePersona = (name: string) =>
    edit({ personas: form.personas.includes(name) ? form.personas.filter((n) => n !== name) : [...form.personas, name] });
  return (
    <fieldset className="max-w-2xl space-y-5" disabled={isReadOnly}>
      <div className="flex items-start gap-2">
        <input
          id="platform-feedback-enabled"
          type="checkbox"
          className="mt-1"
          checked={form.enabled}
          onChange={(e) => edit({ enabled: e.target.checked })}
        />
        <Label htmlFor="platform-feedback-enabled" className="block text-sm font-normal">
          Agents may post platform feedback
          <span className="block text-xs text-muted-foreground">
            Off by default. While off, manage_feedback refuses the platform topic.
          </span>
        </Label>
      </div>
      <ChannelsPicker
        selected={form.channels}
        onChange={(channels) => edit({ channels })}
        help="Each new post is sent to every channel chosen. A repeat of an open post adds to its count and is not sent again."
      />
      <div className="space-y-1.5">
        <p className="text-xs font-medium">Personas that may post</p>
        <label className="flex items-center gap-2 text-sm">
          <input type="radio" name="platform-feedback-who" checked={!only} onChange={() => edit({ personas: [] })} />
          Every persona except service accounts
        </label>
        <label className="flex items-center gap-2 text-sm">
          <input
            type="radio"
            name="platform-feedback-who"
            checked={only}
            onChange={() => {
              if (!only && personas[0]) edit({ personas: [personas[0].name] });
            }}
          />
          Only these personas
        </label>
        <div className="flex flex-wrap gap-x-4 gap-y-1 pl-6">
          {personas.map((p) => (
            <label key={p.name} className={`flex items-center gap-1.5 text-sm ${only ? "" : "opacity-60"}`}>
              <input
                type="checkbox"
                disabled={!only}
                checked={form.personas.includes(p.name)}
                onChange={() => togglePersona(p.name)}
              />
              {p.display_name || p.name}
            </label>
          ))}
        </div>
      </div>
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="platform-feedback-session" className="text-xs">
            Posts per session
          </Label>
          <Input
            id="platform-feedback-session"
            type="number"
            min={1}
            max={100}
            className="w-28"
            value={form.session_limit}
            onChange={(e) => edit({ session_limit: Number(e.target.value) })}
          />
          <p className="text-xs text-muted-foreground">A session past this is refused, naming the limit.</p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="platform-feedback-daily" className="text-xs">
            Posts per persona per day
          </Label>
          <Input
            id="platform-feedback-daily"
            type="number"
            min={1}
            max={10000}
            className="w-28"
            value={form.persona_daily_limit}
            onChange={(e) => edit({ persona_daily_limit: Number(e.target.value) })}
          />
          <p className="text-xs text-muted-foreground">Counted across every session of the persona.</p>
        </div>
      </div>
      <div className="space-y-1 rounded-md border bg-muted/40 p-3 text-sm">
        <p className="font-medium">What a post can hold</p>
        <p className="text-muted-foreground">
          What the agent was trying to do, what blocked it, the tool, a suggestion, and references to the calls
          involved. A post never carries data a tool returned. A post whose text looks like a password, key or token,
          or matches a stored secret, is refused.
        </p>
      </div>
    </fieldset>
  );
}
