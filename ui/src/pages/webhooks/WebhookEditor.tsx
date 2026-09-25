import { useState } from "react";
import { Webhook } from "lucide-react";
import {
  useCreateWebhookSource,
  usePersonas,
  useUpdateWebhookSource,
  useWebhookSource,
} from "@/api/admin/hooks";
import type { WebhookAuthMode, WebhookSource } from "@/api/admin/types";
import { useTableConnections } from "@/api/tables/hooks";
import { LoadingIndicator } from "@/components/LoadingIndicator";
import { PageHeader } from "@/components/patterns/PageHeader";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  ConfigToggle,
} from "@/pages/settings/connections/fields";
import {
  EMPTY_FORM,
  fromSource,
  COMPACT_EVERY_MINUTES,
  MODE_LABELS,
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
    if (creating) {
      create.mutate(body, { onSuccess: (s) => onSaved(s.name) });
    } else {
      update.mutate(body, { onSuccess: (s) => onSaved(s.name) });
    }
  };

  return (
    <div className="space-y-4">
      <EditorHeader source={source} onBack={onBack} />

      <SourceGroup form={form} creating={creating} set={set} />
      <AuthGroup form={form} creating={creating} set={set} />
      <EventsGroup form={form} set={set} />
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
          : "An address an external system posts events to. Its events are queryable as a table as soon as they are acknowledged."
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

type Setter = <K extends keyof WebhookForm>(key: K) => (value: WebhookForm[K]) => void;

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
            help="The sender posts to /hooks/<name>. The table is webhook_<name>, with each - as _."
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
        help="A disabled source answers 404, the same answer as a name that is not a source."
        checked={form.enabled}
        onChange={set("enabled")}
      />
    </ConfigGroup>
  );
}

function AuthGroup({ form, creating, set }: { form: WebhookForm; creating: boolean; set: Setter }) {
  return (
    <ConfigGroup title="Authentication">
      <ConfigSelect
        label="How a request proves it came from the sender"
        value={form.mode}
        onChange={(v) => set("mode")(v as WebhookAuthMode)}
        options={(Object.keys(MODE_LABELS) as WebhookAuthMode[]).map((m) => ({ value: m, label: MODE_LABELS[m] }))}
      />
      <ConfigField
        label={creating ? "Secret" : "New secret"}
        required={creating}
        sensitive
        value={form.secret}
        onChange={set("secret")}
        help={creating ? "Never shown again after it is saved." : "Leave empty to keep the stored secret."}
      />
      {!creating && form.secret !== "" && (
        <ConfigField
          label="Keep accepting the previous secret for (hours)"
          type="number"
          value={form.rotationOverlapHours}
          onChange={set("rotationOverlapHours")}
          help="Time to move the sender to the new secret. 0 stops accepting the previous one at once."
        />
      )}
      {form.mode === "hmac" && <HMACFields form={form} set={set} />}
      {form.mode === "header_token" && (
        <ConfigField label="Header" required mono value={form.header} onChange={set("header")} placeholder="X-Webhook-Token" />
      )}
      {form.mode === "basic" && (
        <ConfigField label="Username" required value={form.username} onChange={set("username")} />
      )}
      {form.mode === "path_token" && (
        <p className="text-xs text-muted-foreground">
          The sender posts to /hooks/&lt;name&gt;/&lt;secret&gt;. Use this only for a sender that can set nothing but a
          URL: the secret is in every log that records addresses.
        </p>
      )}
      <ConfigSelect
        label="CloudEvents handshake"
        value={form.handshake}
        onChange={set("handshake")}
        options={[
          { value: "none", label: "None" },
          { value: "cloudevents", label: "Answer the CloudEvents OPTIONS handshake" },
        ]}
      />
    </ConfigGroup>
  );
}

function HMACFields({ form, set }: { form: WebhookForm; set: Setter }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <ConfigField
        label="Signature header"
        required
        mono
        value={form.signatureHeader}
        onChange={set("signatureHeader")}
        placeholder="X-Signature"
      />
      <ConfigField label="Signature prefix" mono value={form.prefix} onChange={set("prefix")} placeholder="sha256=" />
      <ConfigSelect
        label="Algorithm"
        value={form.algorithm}
        onChange={set("algorithm")}
        options={[
          { value: "sha256", label: "SHA-256" },
          { value: "sha1", label: "SHA-1" },
        ]}
      />
      <ConfigSelect
        label="Encoding"
        value={form.encoding}
        onChange={set("encoding")}
        options={[
          { value: "hex", label: "Hex" },
          { value: "base64", label: "Base64" },
        ]}
      />
      <ConfigField
        label="Timestamp header"
        mono
        value={form.timestampHeader}
        onChange={set("timestampHeader")}
        help="Refuses a request whose timestamp is outside the tolerance, even with a valid signature."
      />
      {form.timestampHeader.trim() !== "" && (
        <>
          <ConfigField
            label="Tolerance (seconds)"
            type="number"
            value={form.toleranceSeconds}
            onChange={set("toleranceSeconds")}
            placeholder="300"
          />
          <ConfigSelect
            label="What is signed"
            value={form.signed}
            onChange={set("signed")}
            options={[
              { value: "body", label: "The body" },
              { value: "timestamp.body", label: "The timestamp, a dot, then the body" },
            ]}
          />
        </>
      )}
    </div>
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
        <ConfigField label="Largest body (bytes)" type="number" value={form.maxBodyBytes} onChange={set("maxBodyBytes")} placeholder="1048576" />
        <ConfigField label="Buffer limit (events)" type="number" value={form.bufferLimit} onChange={set("bufferLimit")} placeholder="50000" />
        <ConfigField label="Write every (ms)" type="number" value={form.flushMaxIntervalMs} onChange={set("flushMaxIntervalMs")} placeholder="1000" />
        <ConfigField label="Or every (events)" type="number" value={form.flushMaxEvents} onChange={set("flushMaxEvents")} placeholder="5000" />
        <ConfigField label="Requests per minute" type="number" value={form.rateLimitPerMinute} onChange={set("rateLimitPerMinute")} placeholder="No limit" />
        <ConfigField label="Burst" type="number" value={form.rateLimitBurst} onChange={set("rateLimitBurst")} />
        <ConfigField
          label="Keep raw events (days)"
          type="number"
          value={form.rawRetentionDays}
          onChange={set("rawRetentionDays")}
          placeholder="7"
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
    </ConfigGroup>
  );
}
