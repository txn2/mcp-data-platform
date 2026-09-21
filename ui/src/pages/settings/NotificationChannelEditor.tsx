import { AlertTriangle, CheckCircle2, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ConfigField, ConfigToggle } from "./connections/fields";
import { RecipientsEditor } from "./RecipientsEditor";
import {
  type ChannelFormState,
  kindLabel,
  kindHelp,
  usesConnection,
  usesTarget,
} from "./notificationChannelForm";

// The editor for one notification channel, opened from the card's list.
//
// It is its own component because the card's job is the list and the editor's
// is a form whose shape changes per kind; together they were one function with
// a branch per field, which is the shape the complexity limit exists to
// refuse.

// TestOutcome is what the channel's upstream answered to a test send.
export interface TestOutcome {
  ok: boolean;
  detail: string;
}

export interface NotificationChannelEditorProps {
  form: ChannelFormState;
  // kinds is what this deployment can deliver to, as the API reports it, so
  // the form offers what will work rather than what compiles.
  kinds: string[];
  // isNew distinguishes a channel being created from one being edited: a test
  // delivers through what is stored, so it is offered only for the latter.
  isNew: boolean;
  isReadOnly: boolean;
  saving: boolean;
  testing: boolean;
  deleting: boolean;
  saveError: string | null;
  testResult: TestOutcome | null;
  onChange: (patch: Partial<ChannelFormState>) => void;
  onSave: () => void;
  onTest: () => void;
  onDelete: () => void;
  onCancel: () => void;
}

export function NotificationChannelEditor({
  form,
  kinds,
  isNew,
  isReadOnly,
  saving,
  testing,
  deleting,
  saveError,
  testResult,
  onChange,
  onSave,
  onTest,
  onDelete,
  onCancel,
}: NotificationChannelEditorProps) {
  return (
    <div className="mt-4 space-y-4 rounded-md border p-4" data-testid="channel-editor">
      <ConfigField
        label="Name"
        help="Lowercase letters, digits and hyphens. This is the name a script or a person names the channel by."
        value={form.name}
        onChange={(v) => onChange({ name: v })}
        placeholder="ops-alerts"
        mono
        required
      />

      <KindPicker kind={form.kind} kinds={kinds} onPick={(k) => onChange({ kind: k })} />

      <ConfigField
        label="Description"
        help="What this channel is for, shown to anyone choosing between channels."
        value={form.description}
        onChange={(v) => onChange({ description: v })}
        placeholder="Operations alerts"
      />

      <KindFields form={form} onChange={onChange} />

      <ConfigToggle
        label="Enabled"
        help="A disabled channel refuses what is sent to it rather than collecting messages nobody will deliver."
        checked={form.enabled}
        onChange={(v) => onChange({ enabled: v })}
      />

      <ConfigToggle
        label="Daily digest"
        help="Collect a day's messages into one bulletin instead of delivering each as it is sent. The channel's mode governs; a sender does not override it."
        checked={form.mode === "daily"}
        onChange={(v) => onChange({ mode: v ? "daily" : "immediate" })}
      />

      <ConfigField
        label="Messages per hour"
        help="How many messages this channel may carry in an hour. Leave empty for the platform default."
        value={form.max_per_hour}
        onChange={(v) => onChange({ max_per_hour: v })}
        type="number"
        placeholder="60"
      />

      <SaveError message={saveError} />
      <TestResultLine result={testResult} />

      <EditorActions
        canSave={Boolean(form.name)}
        isNew={isNew}
        isReadOnly={isReadOnly}
        saving={saving}
        testing={testing}
        deleting={deleting}
        onSave={onSave}
        onTest={onTest}
        onDelete={onDelete}
        onCancel={onCancel}
      />
    </div>
  );
}

// KindPicker chooses the kind and says what that kind needs.
function KindPicker({
  kind,
  kinds,
  onPick,
}: {
  kind: string;
  kinds: string[];
  onPick: (k: string) => void;
}) {
  return (
    <div className="space-y-1.5">
      <span className="text-sm font-medium">Kind</span>
      <div className="flex flex-wrap gap-2">
        {kinds.map((k) => (
          <Button
            key={k}
            type="button"
            size="sm"
            variant={kind === k ? "default" : "outline"}
            onClick={() => onPick(k)}
            data-testid={`channel-kind-${k}`}
          >
            {kindLabel[k] ?? k}
          </Button>
        ))}
      </div>
      <p className="text-sm text-muted-foreground">{kindHelp[kind]}</p>
    </div>
  );
}

// KindFields renders only the fields the chosen kind can use. A field a kind
// cannot use is absent rather than disabled: the API refuses it, so offering
// it would invite a save that cannot succeed.
function KindFields({
  form,
  onChange,
}: {
  form: ChannelFormState;
  onChange: (patch: Partial<ChannelFormState>) => void;
}) {
  return (
    <>
      {usesConnection(form.kind) && (
        <ConfigField
          label="Connection"
          help="The api connection this channel delivers through. Its credential is the bot token or webhook URL, and whoever may reach that connection may send to this channel."
          value={form.connection}
          onChange={(v) => onChange({ connection: v })}
          placeholder="slack-bot"
          mono
          required
        />
      )}
      {usesTarget(form.kind) && (
        <ConfigField
          label="Target channel id"
          help="The chat channel a post is addressed to."
          value={form.target}
          onChange={(v) => onChange({ target: v })}
          placeholder="C0123456789"
          mono
          required
        />
      )}
      {form.kind === "email" && (
        <RecipientsEditor
          recipients={form.recipients}
          onChange={(next) => onChange({ recipients: next })}
          label="Recipients"
          help="Each recipient's own notification preferences still apply: someone who turned email notifications off receives nothing, and every message carries their unsubscribe link."
        />
      )}
    </>
  );
}

// SaveError shows what the API refused, in the words it used.
function SaveError({ message }: { message: string | null }) {
  if (!message) return null;
  return (
    <p className="text-sm text-destructive" data-testid="channel-save-error">
      {message}
    </p>
  );
}

// TestResultLine shows what the channel's upstream answered.
function TestResultLine({ result }: { result: TestOutcome | null }) {
  if (!result) return null;
  const tone = result.ok
    ? "flex items-start gap-1 text-sm text-emerald-600 dark:text-emerald-500"
    : "flex items-start gap-1 text-sm text-destructive";
  const Icon = result.ok ? CheckCircle2 : AlertTriangle;
  return (
    <p className={tone} data-testid="channel-test-result">
      <Icon className="mt-0.5 h-3.5 w-3.5 shrink-0" />
      {result.detail}
    </p>
  );
}

// EditorActions is the row of buttons under the form.
function EditorActions({
  canSave,
  isNew,
  isReadOnly,
  saving,
  testing,
  deleting,
  onSave,
  onTest,
  onDelete,
  onCancel,
}: {
  canSave: boolean;
  isNew: boolean;
  isReadOnly: boolean;
  saving: boolean;
  testing: boolean;
  deleting: boolean;
  onSave: () => void;
  onTest: () => void;
  onDelete: () => void;
  onCancel: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <Button onClick={onSave} disabled={isReadOnly || saving || !canSave}>
        {saving ? "Saving…" : "Save channel"}
      </Button>
      {/* A test and a delete are offered only for a channel that exists: a
          test delivers through what is stored, not through what is on
          screen. */}
      {!isNew && (
        <Button
          variant="outline"
          onClick={onTest}
          disabled={isReadOnly || testing}
          data-testid="channel-test"
        >
          {testing ? "Sending…" : "Send test"}
        </Button>
      )}
      <Button variant="ghost" onClick={onCancel}>
        Cancel
      </Button>
      {!isNew && (
        <Button
          variant="ghost"
          className="ml-auto text-destructive"
          onClick={onDelete}
          disabled={isReadOnly || deleting}
          data-testid="channel-delete"
        >
          <Trash2 className="mr-1 h-4 w-4" />
          Delete
        </Button>
      )}
    </div>
  );
}
