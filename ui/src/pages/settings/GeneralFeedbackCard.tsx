import { useCallback, useEffect, useState } from "react";
import { Megaphone, Users } from "lucide-react";
import { useGeneralFeedback, useSetGeneralFeedback } from "@/api/admin/hooks";
import type {
  GeneralFeedbackSettings,
  GeneralFeedbackView,
} from "@/api/admin/types";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { SettingsCard } from "./panels";
import { ChannelsPicker } from "./ChannelsPicker";
import { RecipientsEditor } from "./RecipientsEditor";
import {
  ErrorBanner,
  ReadOnlyBanner,
  SaveButton,
  SaveFeedbackBanners,
  UpdatedByMeta,
  WarningBanner,
} from "./settingsChrome";
import { useUnsavedChangesGuard } from "@/lib/navigationGuard";

// GeneralFeedbackCard is who is told when someone posts to the General
// feedback channel (#2603): a thread there has no item, so no owner and nobody
// it is shared with, and without this list a post reached nobody.

const EMPTY: GeneralFeedbackSettings = {
  personas: [],
  recipients: [],
  channels: [],
  acknowledge_author: false,
};

function formFrom(v: GeneralFeedbackView): GeneralFeedbackSettings {
  return {
    personas: v.personas ?? [],
    recipients: v.recipients ?? [],
    channels: v.channels ?? [],
    acknowledge_author: v.acknowledge_author,
  };
}

function plural(n: number, one: string, many: string): string {
  return `${n} ${n === 1 ? one : many}`;
}

// NobodyWarning names why the saved list reaches nobody: the chosen personas
// that have no people with an address, when that is the reason.
function NobodyWarning({ view }: { view: GeneralFeedbackView }) {
  const empty = view.persona_options
    .filter((p) => view.personas.includes(p.name) && p.people === 0)
    .map((p) => p.display_name || p.name);
  return (
    <WarningBanner>
      <strong>A new post reaches nobody.</strong>{" "}
      {empty.length > 0 &&
        `${empty.join(", ")} ${empty.length === 1 ? "has" : "have"} no people with an address: nobody whose roles map to ${empty.length === 1 ? "it" : "them"} has signed in. `}
      Choose another persona, add an address, or choose a channel.
    </WarningBanner>
  );
}

// Reach is how many people the saved list reaches, with the addresses on
// request.
function Reach({ people }: { people: string[] }) {
  const [shown, setShown] = useState(false);
  return (
    <div className="space-y-2 rounded-md bg-muted/60 px-3 py-2 text-sm">
      <div className="flex flex-wrap items-center gap-2">
        <Users className="size-4" aria-hidden="true" />
        <span>
          A new post reaches{" "}
          <strong>{plural(people.length, "person", "people")}</strong>.
        </span>
        <Button
          type="button"
          variant="link"
          size="sm"
          className="h-auto p-0"
          onClick={() => setShown((s) => !s)}
        >
          {shown ? "Hide them" : "Show them"}
        </Button>
      </div>
      {shown && (
        <ul
          className="flex flex-wrap gap-x-4 gap-y-1 font-mono text-xs"
          aria-label="People a new post reaches"
        >
          {people.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      )}
    </div>
  );
}

function PersonaPicker({
  view,
  selected,
  disabled,
  onChange,
}: {
  view: GeneralFeedbackView;
  selected: string[];
  disabled: boolean;
  onChange: (next: string[]) => void;
}) {
  const toggle = (name: string) =>
    onChange(
      selected.includes(name)
        ? selected.filter((n) => n !== name)
        : [...selected, name],
    );
  return (
    <fieldset className="space-y-1.5" disabled={disabled}>
      <legend className="mb-1.5 text-xs font-medium">Personas</legend>
      {view.persona_options.map((p) => {
        const checked = selected.includes(p.name);
        return (
          <label key={p.name} className="flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={checked}
              onChange={() => toggle(p.name)}
            />
            {p.display_name || p.name}
            <span
              className={
                checked && p.people === 0
                  ? "text-xs text-amber-700 dark:text-amber-400"
                  : "text-xs text-muted-foreground"
              }
            >
              {plural(p.people, "person", "people")}
            </span>
          </label>
        );
      })}
      <p className="text-xs text-muted-foreground">
        Everyone who has signed in with roles that map to a chosen persona.
        Someone who only uses an API key has no address and is not counted.
      </p>
    </fieldset>
  );
}

// useListDraft is the card's edit state: the form seeded from the saved list,
// whether it changed, and the outcome of the last save.
function useListDraft(view: GeneralFeedbackView | undefined) {
  const save = useSetGeneralFeedback();
  const [form, setForm] = useState<GeneralFeedbackSettings>(EMPTY);
  const [dirty, setDirty] = useState(false);
  useUnsavedChangesGuard(dirty);
  const [saveSuccess, setSaveSuccess] = useState(false);
  const [saveError, setSaveError] = useState<string | null>(null);

  useEffect(() => {
    if (!view) return;
    setForm(formFrom(view));
    setDirty(false);
  }, [view]);

  const edit = useCallback((patch: Partial<GeneralFeedbackSettings>) => {
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
      onError: (err) =>
        setSaveError(err instanceof Error ? err.message : "Failed to save"),
    });
  }, [form, save]);

  return {
    form,
    dirty,
    saveSuccess,
    saveError,
    saving: save.isPending,
    edit,
    handleSave,
  };
}

// ListNotices states why the section cannot do its job: the saved list
// reaches nobody, the settings are read-only, or the list did not load.
function ListNotices({
  view,
  isReadOnly,
  loadFailed,
  onRetry,
}: {
  view: GeneralFeedbackView | undefined;
  isReadOnly: boolean;
  loadFailed: boolean;
  onRetry: () => void;
}) {
  return (
    <>
      {view?.warnings.includes("reaches_nobody") && (
        <NobodyWarning view={view} />
      )}
      {isReadOnly && <ReadOnlyBanner />}
      {loadFailed && (
        <ErrorBanner
          message="Failed to load the General feedback list."
          onRetry={onRetry}
        />
      )}
    </>
  );
}

export function GeneralFeedbackCard({ isReadOnly }: { isReadOnly: boolean }) {
  const {
    data: view,
    isLoading,
    error: loadError,
    refetch,
  } = useGeneralFeedback();
  const d = useListDraft(view);
  return (
    <SettingsCard
      icon={Megaphone}
      title="General feedback"
      description="Who is told when someone posts to the General channel on the Feedback page. These people also hear about every reply, as an item's owner does."
      notices={
        <ListNotices
          view={view}
          isReadOnly={isReadOnly}
          loadFailed={!!loadError}
          onRetry={() => void refetch()}
        />
      }
      feedback={<SaveFeedbackBanners saveError={d.saveError} dirty={d.dirty} />}
      action={
        <>
          {view && !view.updated_by && (
            <span className="text-xs text-muted-foreground">Default</span>
          )}
          <UpdatedByMeta
            updatedBy={view?.updated_by}
            updatedAt={view?.updated_at}
          />
          {!isReadOnly && (
            <SaveButton
              dirty={d.dirty}
              saving={d.saving}
              saveSuccess={d.saveSuccess}
              onSave={d.handleSave}
            />
          )}
        </>
      }
    >
      {isLoading || !view ? (
        <p className="text-sm text-muted-foreground">Loading...</p>
      ) : (
        <ListForm
          view={view}
          form={d.form}
          isReadOnly={isReadOnly}
          onEdit={d.edit}
        />
      )}
    </SettingsCard>
  );
}

// ListForm edits the list: personas, recipients, channels and the
// acknowledgement, under how many people the saved list reaches.
function ListForm({
  view,
  form,
  isReadOnly,
  onEdit: edit,
}: {
  view: GeneralFeedbackView;
  form: GeneralFeedbackSettings;
  isReadOnly: boolean;
  onEdit: (patch: Partial<GeneralFeedbackSettings>) => void;
}) {
  const reachesNobody = view.warnings.includes("reaches_nobody");
  return (
    <div className="max-w-2xl space-y-5">
      {!reachesNobody && view.people.length > 0 && (
        <Reach people={view.people} />
      )}
      <PersonaPicker
        view={view}
        selected={form.personas}
        disabled={isReadOnly}
        onChange={(personas) => edit({ personas })}
      />
      <RecipientsEditor
        recipients={form.recipients}
        onChange={(recipients) => edit({ recipients })}
        placeholder="data-requests@example.com"
        help="Addresses told as well, such as a team list. Each person's own notification settings still apply."
      />
      <ChannelsPicker
        selected={form.channels}
        onChange={(channels) => edit({ channels })}
        help="A new post is also sent to each channel chosen here. Replies are not."
      />
      <div className="flex items-start gap-2">
        <input
          id="general-feedback-ack"
          type="checkbox"
          className="mt-1"
          checked={form.acknowledge_author}
          disabled={isReadOnly}
          onChange={(e) => edit({ acknowledge_author: e.target.checked })}
        />
        <Label
          htmlFor="general-feedback-ack"
          className="block text-sm font-normal"
        >
          Tell the author it was received
          <span className="block text-xs text-muted-foreground">
            Sends the person who posted an email saying their post went to the
            people who handle feedback. It does not name them.
          </span>
        </Label>
      </div>
    </div>
  );
}
