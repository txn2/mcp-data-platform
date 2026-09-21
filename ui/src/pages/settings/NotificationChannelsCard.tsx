import { useState, useCallback, useMemo } from "react";
import { Send, Plus, AlertTriangle } from "lucide-react";
import {
  useNotificationChannels,
  useSetNotificationChannel,
  useDeleteNotificationChannel,
  useTestNotificationChannel,
  type NotificationChannel,
} from "@/api/admin/hooks";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { NotificationChannelEditor, type TestOutcome } from "./NotificationChannelEditor";
import {
  type ChannelFormState,
  blankChannelForm,
  channelFormFrom,
  channelInputFrom,
  kindLabel,
} from "./notificationChannelForm";
import { SettingsCard } from "./panels";

// The notification-channels section (#1720).
//
// A channel is a destination a report or an alert is sent to: a Mattermost
// channel, an incoming webhook, or a named list of addresses. It holds no
// credential of its own — the two HTTP kinds name an api connection, and the
// bot token lives there, encrypted, with every other upstream credential —
// which is why the editor asks for a connection rather than for a token.

export function NotificationChannelsCard({ isReadOnly }: { isReadOnly: boolean }) {
  const { data, isLoading, error: loadError } = useNotificationChannels();
  const save = useSetNotificationChannel();
  const remove = useDeleteNotificationChannel();
  const test = useTestNotificationChannel();

  // editing is the channel name open in the editor, "" for a new one, and
  // null for none. A new channel is distinguishable from an edit so its name
  // field stays writable and no test is offered for it.
  const [editing, setEditing] = useState<string | null>(null);
  const [form, setForm] = useState<ChannelFormState>(blankChannelForm);
  const [saveError, setSaveError] = useState<string | null>(null);
  const [testResult, setTestResult] = useState<TestOutcome | null>(null);

  const channels = data?.channels ?? [];
  const kinds = useMemo(() => data?.kinds ?? Object.keys(kindLabel), [data]);

  const openNew = useCallback(() => {
    setEditing("");
    setForm(blankChannelForm);
    setSaveError(null);
    setTestResult(null);
  }, []);

  const openExisting = useCallback((ch: NotificationChannel) => {
    setEditing(ch.name);
    setForm(channelFormFrom(ch));
    setSaveError(null);
    setTestResult(null);
  }, []);

  const close = useCallback(() => {
    setEditing(null);
    setTestResult(null);
  }, []);

  const change = useCallback((patch: Partial<ChannelFormState>) => {
    setForm((prev) => ({ ...prev, ...patch }));
    setSaveError(null);
    setTestResult(null);
  }, []);

  const handleSave = useCallback(() => {
    setSaveError(null);
    save.mutate(
      { name: form.name, input: channelInputFrom(form) },
      {
        onSuccess: () => close(),
        onError: (err: Error) => setSaveError(err.message),
      },
    );
  }, [form, save, close]);

  const handleTest = useCallback(() => {
    setTestResult(null);
    test.mutate(form.name, {
      onSuccess: (res) => setTestResult({ ok: res.delivered, detail: res.detail }),
      // The transport's own words: "not_in_channel" is the only thing that
      // tells the administrator what to fix.
      onError: (err: Error) => setTestResult({ ok: false, detail: err.message }),
    });
  }, [form.name, test]);

  const handleDelete = useCallback(() => {
    if (editing) {
      remove.mutate(editing, { onSuccess: () => close() });
    }
  }, [editing, remove, close]);

  return (
    <SettingsCard
      icon={Send}
      title="Notification Channels"
      description="Where reports and alerts are sent: a Mattermost channel, an incoming webhook, or a list of email addresses. A channel holds no credential — the chat and webhook kinds name an api connection, and the token lives there."
      action={
        !isReadOnly && (
          <Button size="sm" variant="outline" onClick={openNew} data-testid="channel-new">
            <Plus className="mr-1 h-4 w-4" />
            Add channel
          </Button>
        )
      }
    >
      <ChannelList
        channels={channels}
        isLoading={isLoading}
        loadFailed={Boolean(loadError)}
        editorOpen={editing !== null}
        onOpen={openExisting}
      />

      {editing !== null && (
        <NotificationChannelEditor
          form={form}
          kinds={kinds}
          isNew={editing === ""}
          isReadOnly={isReadOnly}
          saving={save.isPending}
          testing={test.isPending}
          deleting={remove.isPending}
          saveError={saveError}
          testResult={testResult}
          onChange={change}
          onSave={handleSave}
          onTest={handleTest}
          onDelete={handleDelete}
          onCancel={close}
        />
      )}
    </SettingsCard>
  );
}

// ChannelList is the card's list: what is configured, or the reason nothing
// is shown.
function ChannelList({
  channels,
  isLoading,
  loadFailed,
  editorOpen,
  onOpen,
}: {
  channels: NotificationChannel[];
  isLoading: boolean;
  loadFailed: boolean;
  // editorOpen suppresses the empty-state line while a first channel is being
  // created: "no channels are configured" under the form creating one reads
  // as a refusal.
  editorOpen: boolean;
  onOpen: (ch: NotificationChannel) => void;
}) {
  if (isLoading) {
    return <p className="text-sm text-muted-foreground">Loading channels…</p>;
  }
  if (loadFailed) {
    return <p className="text-sm text-destructive">Could not read the configured channels.</p>;
  }
  if (channels.length === 0) {
    if (editorOpen) return null;
    return (
      <p className="text-sm text-muted-foreground">
        No channels are configured. Until one is, nothing can be posted to a chat channel or a
        mailing list from a script, a session or an asset page.
      </p>
    );
  }
  return (
    <ul className="divide-y rounded-md border" data-testid="channel-list">
      {channels.map((ch) => (
        <ChannelRow key={ch.name} channel={ch} onOpen={onOpen} />
      ))}
    </ul>
  );
}

// ChannelRow is one channel in the list. Row click opens the editor, as every
// portal list does.
function ChannelRow({
  channel: ch,
  onOpen,
}: {
  channel: NotificationChannel;
  onOpen: (ch: NotificationChannel) => void;
}) {
  return (
    <li>
      <button
        type="button"
        onClick={() => onOpen(ch)}
        className="flex w-full items-start justify-between gap-3 px-3 py-2 text-left hover:bg-muted/50"
        data-testid={`channel-row-${ch.name}`}
      >
        <span className="min-w-0">
          <span className="flex items-center gap-2">
            <span className="truncate font-medium">{ch.name}</span>
            <Badge variant="outline">{kindLabel[ch.kind] ?? ch.kind}</Badge>
            {!ch.enabled && <Badge variant="secondary">disabled</Badge>}
            {ch.mode === "daily" && <Badge variant="secondary">daily digest</Badge>}
          </span>
          {ch.description && (
            <span className="mt-0.5 block truncate text-sm text-muted-foreground">
              {ch.description}
            </span>
          )}
          {ch.warnings?.map((w) => (
            <span
              key={w}
              className="mt-1 flex items-start gap-1 text-sm text-amber-600 dark:text-amber-500"
            >
              <AlertTriangle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
              {w}
            </span>
          ))}
        </span>
        <span className="shrink-0 text-sm text-muted-foreground">
          {ch.connection || `${ch.recipients?.length ?? 0} recipients`}
        </span>
      </button>
    </li>
  );
}
