import { INDEX_POLICY_LABELS } from "@/pages/resources/parts/IndexPolicyField";
import { useState } from "react";
import { AlertTriangle, Webhook } from "lucide-react";
import {
  useCreateWebhookSource,
  usePersonas,
  useUpdateWebhookSource,
  useWebhookSource,
} from "@/api/admin/hooks";
import type { WebhookSource } from "@/api/admin/types";
import { useTableConnections } from "@/api/tables/hooks";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  ConfigToggle,
} from "@/pages/settings/connections/fields";
import { MailboxGroup } from "./MailboxGroup";
import { AuthGroup } from "./WebhookAuthGroup";
import {
  EMPTY_FORM,
  fromSource,
  COMPACT_EVERY_MINUTES,
  problems,
  toInput,
  type WebhookForm,
  windowLength,
} from "./webhookForm";

// WebhookEditor creates a source, or changes one (#1870). The name, the
// connection and the persona are chosen once: the table is created on that
// connection under a name derived from the source's, and its compacted
// windows are written to that persona's library.

export function WebhookEditor({
  name,
  onBack,
  onSaved,
}: {
  /** name is the source being edited, or undefined to create one. */
  name?: string;
  onBack: () => void;
  onSaved: (name: string) => void;
}) {
  const existing = useWebhookSource(name ?? "");
  if (name && existing.isLoading) {
    return (
      <div className="flex justify-center py-16">
        <LoadingIndicator />
      </div>
    );
  }
  if (name && (existing.error || !existing.data)) {
    return (
      <div className="space-y-4">
        <PageHeader backLabel="Webhooks" onBack={onBack} title={name} />
        <Alert variant="destructive" className="py-2">
          <AlertDescription>This source could not be read.</AlertDescription>
        </Alert>
      </div>
    );
  }
  return <EditorForm source={existing.data?.source} onBack={onBack} onSaved={onSaved} />;
}

function EditorForm({
  source,
  onBack,
  onSaved,
}: {
  source?: WebhookSource;
  onBack: () => void;
  onSaved: (name: string) => void;
}) {
  const creating = source === undefined;
  const [form, setForm] = useState<WebhookForm>(source ? fromSource(source) : EMPTY_FORM);
  const [shown, setShown] = useState<string[]>([]);
  const [warned, setWarned] = useState<{ name: string; warnings: string[] } | null>(null);
  const create = useCreateWebhookSource();
  const update = useUpdateWebhookSource(source?.name ?? "");
  const saving = create.isPending || update.isPending;
  const error = (create.error ?? update.error) as Error | null;

  const set = <K extends keyof WebhookForm>(key: K) => (value: WebhookForm[K]) =>
    setForm((f) => ({ ...f, [key]: value }));

  const save = () => {
    const found = problems(form, creating);
    setShown(found);
    if (found.length > 0) return;
    const body = toInput(form, creating);
    // A save that came back with warnings stays here until they are read.
    const done = (s: WebhookSource) =>
      s.warnings?.length ? setWarned({ name: s.name, warnings: s.warnings }) : onSaved(s.name);
    if (creating) {
      create.mutate(body, { onSuccess: done });
    } else {
      update.mutate(body, { onSuccess: done });
    }
  };

  if (warned) {
    return (
      <div className="space-y-4">
        <EditorHeader source={source} onBack={onBack} />
        <SavedWithWarnings warnings={warned.warnings} onOpen={() => onSaved(warned.name)} />
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <EditorHeader source={source} onBack={onBack} />

      <SourceGroup form={form} creating={creating} set={set} />
      <InputGroups form={form} creating={creating} set={set} />
      <LimitsGroup form={form} set={set} />

      <Refusals shown={shown} error={error} />

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onBack} disabled={saving}>
          Cancel
        </Button>
        <Button type="button" onClick={save} disabled={saving}>
          {saveLabel(creating, saving)}
        </Button>
      </div>
      {creating && saving && (
        <p className="text-right text-xs text-muted-foreground">
          Creating the source&rsquo;s table and checking the connection can hold it.
        </p>
      )}
    </div>
  );
}

/** SavedWithWarnings is what a save that succeeded with warnings shows: the
 * warnings, and the way on to the source. */
export function SavedWithWarnings({ warnings, onOpen }: { warnings: string[]; onOpen: () => void }) {
  return (
    <Alert className="border-amber-500/50 py-3 text-amber-900 dark:text-amber-200">
      <AlertTriangle />
      <AlertTitle>Saved, with warnings</AlertTitle>
      <AlertDescription>
        <ul className="list-disc space-y-1 pl-4">
          {warnings.map((w) => (
            <li key={w}>{w}</li>
          ))}
        </ul>
        <Button type="button" size="xs" className="mt-3" onClick={onOpen}>
          Open the source
        </Button>
      </AlertDescription>
    </Alert>
  );
}

function saveLabel(creating: boolean, saving: boolean): string {
  if (saving) return "Saving...";
  return creating ? "Create source" : "Save changes";
}

function EditorHeader({ source, onBack }: { source?: WebhookSource; onBack: () => void }) {
  return (
    <PageHeader
      backLabel="Webhooks"
      onBack={onBack}
      icon={Webhook}
      title={source ? `Edit ${source.name}` : "New webhook source"}
      subtitle={
        source
          ? "The name, connection and persona were fixed when the source was created."
          : "Events posted by an external system, or the mail a mailbox receives, queryable as a table."
      }
    />
  );
}

/** Refusals shows what the form found, then what the platform answered. */
function Refusals({ shown, error }: { shown: string[]; error: Error | null }) {
  return (
    <>
      {shown.length > 0 && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>
            <ul className="list-disc pl-4">
              {shown.map((p) => (
                <li key={p}>{p}</li>
              ))}
            </ul>
          </AlertDescription>
        </Alert>
      )}
      {error && (
        <Alert variant="destructive" className="py-2">
          <AlertDescription>{error.message}</AlertDescription>
        </Alert>
      )}
    </>
  );
}

export type Setter = <K extends keyof WebhookForm>(key: K) => (value: WebhookForm[K]) => void;

/** InputGroups are the settings of the source's input: a mailbox's, or a
 * posted source's authentication and events. A form's source has none: the
 * platform owns them (#2178). */
function InputGroups({ form, creating, set }: { form: WebhookForm; creating: boolean; set: Setter }) {
  if (form.input === "imap") return <MailboxGroup form={form} creating={creating} set={set} />;
  if (form.input !== "http") return null;
  return (
    <>
      <AuthGroup form={form} creating={creating} set={set} />
      <EventsGroup form={form} set={set} />
    </>
  );
}

function SourceGroup({ form, creating, set }: { form: WebhookForm; creating: boolean; set: Setter }) {
  const connections = useTableConnections(creating);
  const personas = usePersonas(creating);
  return (
    <ConfigGroup title="Source">
      {creating ? (
        <>
          <ConfigField
            label="Name"
            required
            mono
            value={form.name}
            onChange={set("name")}
            placeholder="email-events"
            help="The table is webhook_<name>, with each - as _. A posted source receives at /hooks/<name>."
          />
          <ConfigSelect
            label="Events come from"
            value={form.input}
            onChange={(v) => set("input")(v as WebhookForm["input"])}
            options={[
              { value: "http", label: "Requests posted to the source" },
              { value: "imap", label: "A mailbox the platform checks (IMAP)" },
            ]}
            help="A mailbox source files each message from an allowed sender as one event, its attachments as resources the event names."
          />
          <ConfigSelect
            label="Connection"
            value={form.connection}
            onChange={set("connection")}
            options={[
              { value: "", label: connections.isLoading ? "Loading..." : "Choose a connection" },
              ...(connections.data?.connections ?? []).map((c) => ({
                value: c.name,
                label: `${c.name} (${c.catalog}.${c.schema})`,
              })),
            ]}
            help="The Trino connection whose scratch catalog holds the table. Its catalog must read the managed-resources store and allow register_partition."
          />
          <ConfigSelect
            label="Who sees the compacted files"
            value={form.persona}
            onChange={set("persona")}
            options={[
              { value: "", label: "Administrators only" },
              ...(personas.data?.personas ?? []).map((p) => ({
                value: p.name,
                label: p.display_name || p.name,
              })),
            ]}
            help="Each compacted window's Parquet file is a resource in this persona's library. Querying the table is decided by the connection."
          />
        </>
      ) : null}
      <ConfigToggle
        label="Enabled"
        help={
          form.input === "imap"
            ? "A disabled source's mailbox is not checked."
            : "A disabled source answers 404, the same answer as a name that is not a source."
        }
        checked={form.enabled}
        onChange={set("enabled")}
      />
    </ConfigGroup>
  );
}

function EventsGroup({ form, set }: { form: WebhookForm; set: Setter }) {
  return (
    <ConfigGroup title="Events">
      <p className="text-xs text-muted-foreground">
        JSON paths: $ for the body, .name or [&apos;name&apos;] for a member, [n] for an element.
      </p>
      <div className="grid gap-3 sm:grid-cols-2">
        <ConfigField
          label="Split"
          mono
          value={form.split}
          onChange={set("split")}
          placeholder="$.events"
          help="A path to an array makes each element its own event. Empty: the body is one event."
        />
        <ConfigField
          label="Event id"
          mono
          value={form.eventIdPath}
          onChange={set("eventIdPath")}
          placeholder="$.id"
          help="Duplicates are removed on it. Empty: a hash of the event."
        />
        <ConfigField label="Event type" mono value={form.eventTypePath} onChange={set("eventTypePath")} placeholder="$.type" />
        <ConfigField
          label="Key"
          mono
          value={form.keyPath}
          onChange={set("keyPath")}
          placeholder="$.contact.id"
          help="The entity an event is about, so a reader can keep the latest per key."
        />
      </div>
    </ConfigGroup>
  );
}

function LimitsGroup({ form, set }: { form: WebhookForm; set: Setter }) {
  return (
    <ConfigGroup title="Limits and retention">
      <p className="text-xs text-muted-foreground">Leave a field empty for its default.</p>
      <ConfigSelect
        label="Compact every"
        value={form.compactEveryMinutes}
        onChange={set("compactEveryMinutes")}
        options={COMPACT_EVERY_MINUTES.map((m) => ({
          value: String(m),
          label: windowLength(m),
        }))}
        help="Events are grouped into windows of this length, and each window becomes one Parquet file once it has ended. Every event is queryable as soon as it is accepted either way; a shorter window compacts sooner, into more files."
      />
      <div className="grid gap-3 sm:grid-cols-3">
        {form.input === "http" && <RequestLimits form={form} set={set} />}
        <ConfigField
          label="Keep raw events (days)"
          type="number"
          value={form.rawRetentionDays}
          onChange={set("rawRetentionDays")}
          placeholder="1"
        />
        <ConfigField
          label="Keep compacted files (days)"
          type="number"
          value={form.compactedRetentionDays}
          onChange={set("compactedRetentionDays")}
          placeholder="400"
          help="0 keeps them forever."
        />
      </div>
      <ConfigSelect
        label="Search"
        value={form.index}
        onChange={set("index")}
        options={[
          { value: "default", label: "Default" },
          { value: "full", label: INDEX_POLICY_LABELS.full },
          { value: "metadata", label: INDEX_POLICY_LABELS.metadata },
          { value: "none", label: INDEX_POLICY_LABELS.none },
        ]}
        help="How search indexes each compacted file, from the next compaction on. Default leaves it to this deployment's resources.index rules."
      />
    </ConfigGroup>
  );
}

/** RequestLimits bound what a posted source accepts; a mailbox source has none. */
function RequestLimits({ form, set }: { form: WebhookForm; set: Setter }) {
  return (
    <>
      <ConfigField label="Largest body (bytes)" type="number" value={form.maxBodyBytes} onChange={set("maxBodyBytes")} placeholder="1048576" />
      <ConfigField label="Buffer limit (events)" type="number" value={form.bufferLimit} onChange={set("bufferLimit")} placeholder="50000" />
      <ConfigField label="Write every (ms)" type="number" value={form.flushMaxIntervalMs} onChange={set("flushMaxIntervalMs")} placeholder="1000" />
      <ConfigField label="Or every (events)" type="number" value={form.flushMaxEvents} onChange={set("flushMaxEvents")} placeholder="5000" />
      <ConfigField label="Requests per minute" type="number" value={form.rateLimitPerMinute} onChange={set("rateLimitPerMinute")} placeholder="No limit" />
      <ConfigField label="Burst" type="number" value={form.rateLimitBurst} onChange={set("rateLimitBurst")} />
    </>
  );
}
