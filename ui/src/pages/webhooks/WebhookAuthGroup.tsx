import type { WebhookAuthMode } from "@/api/admin/types";
import { ConfigField, ConfigGroup, ConfigSelect } from "@/pages/settings/connections/fields";
import type { Setter } from "./WebhookEditor";
import {
  HANDSHAKES,
  HTTP_MODES,
  isTemplate,
  MODE_LABELS,
  timestampSource,
  type WebhookForm,
  WEBHOOK_PRESETS,
} from "./webhookForm";

// The webhook source editor's Authentication group (#1870, #2223): the sender
// a source is configured for by preset, or the mode and settings it is
// configured with by hand, and the setup check it answers.

const PRESET_HELP =
  "The platform verifies requests and answers this sender's setup check the way its documentation describes.";
const NO_PRESET_HELP = "Choose a sender to have its signature and setup check configured for you.";

/** secretLabel names the secret as the chosen sender names it. */
function secretLabel(form: WebhookForm, creating: boolean): string {
  const preset = WEBHOOK_PRESETS[form.preset];
  if (preset) return preset.secretLabel;
  return creating ? "Secret" : "New secret";
}

/** secretHelp says what the secret is and whether it is kept. */
function secretHelp(form: WebhookForm, creating: boolean): string {
  const kind = !WEBHOOK_PRESETS[form.preset] && form.mode === "ed25519" ? "The sender's Ed25519 public key, in hex. " : "";
  return kind + (creating ? "Never shown again after it is saved." : "Leave empty to keep the stored secret.");
}

export function AuthGroup({ form, creating, set }: { form: WebhookForm; creating: boolean; set: Setter }) {
  const preset = WEBHOOK_PRESETS[form.preset];
  return (
    <ConfigGroup title="Authentication">
      <ConfigSelect
        label="Sender"
        value={form.preset}
        onChange={set("preset")}
        options={[
          { value: "", label: "Configure by hand" },
          ...Object.entries(WEBHOOK_PRESETS).map(([value, p]) => ({ value, label: p.label })),
        ]}
        help={preset ? PRESET_HELP : NO_PRESET_HELP}
      />
      {!preset && (
        <ConfigSelect
          label="How a request proves it came from the sender"
          value={form.mode}
          onChange={(v) => set("mode")(v as WebhookAuthMode)}
          options={HTTP_MODES.map((m) => ({ value: m, label: MODE_LABELS[m] }))}
        />
      )}
      <ConfigField
        label={secretLabel(form, creating)}
        required={creating}
        sensitive
        value={form.secret}
        onChange={set("secret")}
        help={secretHelp(form, creating)}
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
      {!preset && <ModeFields form={form} set={set} />}
      {!preset && (
        <ConfigSelect label="Setup check" value={form.handshake} onChange={set("handshake")} options={HANDSHAKES} />
      )}
    </ConfigGroup>
  );
}

/** ModeFields are the settings a hand-configured mode reads. */
function ModeFields({ form, set }: { form: WebhookForm; set: Setter }) {
  switch (form.mode) {
    case "hmac":
      return <HMACFields form={form} set={set} />;
    case "ed25519":
      return <Ed25519Fields form={form} set={set} />;
    case "header_token":
    case "jwt":
      return (
        <ConfigField
          label="Header"
          required
          mono
          value={form.header}
          onChange={set("header")}
          placeholder={form.mode === "jwt" ? "Authorization" : "X-Webhook-Token"}
          help={form.mode === "jwt" ? "An HS256 token signed with the secret, with or without Bearer. Its exp is honored." : undefined}
        />
      );
    case "body_token":
      return (
        <ConfigField
          label="Token path"
          required
          mono
          value={form.tokenPath}
          onChange={set("tokenPath")}
          placeholder="token"
          help="Where in the JSON or form body the token is. A path through [*], such as value[*].clientState, requires every element to carry it."
        />
      );
    case "basic":
      return <ConfigField label="Username" required value={form.username} onChange={set("username")} />;
    case "path_token":
      return (
        <p className="text-xs text-muted-foreground">
          The sender posts to /hooks/&lt;name&gt;/&lt;secret&gt;. Use this only for a sender that can set nothing but a
          URL: the secret is in every log that records addresses.
        </p>
      );
  }
  return null;
}

function HMACFields({ form, set }: { form: WebhookForm; set: Setter }) {
  const stripe = form.headerFormat === "stripe";
  const pattern = form.headerPattern.trim() !== "";
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <ConfigField
        label="Signature header"
        required
        mono
        value={form.signatureHeader}
        onChange={set("signatureHeader")}
        placeholder={stripe ? "Stripe-Signature" : "X-Signature"}
      />
      {!pattern && (
        <ConfigSelect
          label="Header format"
          value={form.headerFormat}
          onChange={set("headerFormat")}
          options={[
            { value: "", label: "Prefix and signature" },
            { value: "stripe", label: "Stripe" },
          ]}
        />
      )}
      <ConfigSelect
        label="Algorithm"
        value={form.algorithm}
        onChange={set("algorithm")}
        options={[
          { value: "sha256", label: "SHA-256" },
          { value: "sha1", label: "SHA-1" },
          { value: "sha512", label: "SHA-512" },
        ]}
      />
      <EncodingSelect form={form} set={set} />
      <ConfigSelect
        label="Secret encoding"
        value={form.secretEncoding}
        onChange={set("secretEncoding")}
        options={[
          { value: "", label: "As written" },
          { value: "base64", label: "Base64" },
        ]}
      />
      {!stripe && (
        <>
          {!pattern && (
            <ConfigField label="Prefix" mono value={form.prefix} onChange={set("prefix")} placeholder="sha256=" />
          )}
          <ConfigField
            label="Header pattern"
            mono
            value={form.headerPattern}
            onChange={set("headerPattern")}
            placeholder="{timestamp}|{signature}"
            help="Lays out the whole signature header over {signature} and {timestamp}. Replaces the prefix and the header format."
          />
          <TimestampFields form={form} set={set} />
        </>
      )}
      {stripe && <ToleranceField form={form} set={set} />}
    </div>
  );
}

function Ed25519Fields({ form, set }: { form: WebhookForm; set: Setter }) {
  return (
    <div className="grid gap-3 sm:grid-cols-2">
      <ConfigField
        label="Signature header"
        required
        mono
        value={form.signatureHeader}
        onChange={set("signatureHeader")}
        placeholder="X-Signature-Ed25519"
      />
      <EncodingSelect form={form} set={set} />
      <TimestampFields form={form} set={set} />
    </div>
  );
}

function EncodingSelect({ form, set }: { form: WebhookForm; set: Setter }) {
  return (
    <ConfigSelect
      label="Encoding"
      value={form.encoding}
      onChange={set("encoding")}
      options={[
        { value: "hex", label: "Hex" },
        { value: "base64", label: "Base64" },
      ]}
    />
  );
}

/** TimestampFields are where the signed timestamp is read, the window it is
 * checked in, and the bytes the signature covers. */
function TimestampFields({ form, set }: { form: WebhookForm; set: Setter }) {
  const template = isTemplate(form.signed);
  return (
    <>
      <ConfigField
        label="Timestamp header"
        mono
        value={form.timestampHeader}
        onChange={set("timestampHeader")}
        help="Refuses a request whose timestamp is outside the tolerance, even with a valid signature."
      />
      <ConfigField
        label="Timestamp path"
        mono
        value={form.timestampPath}
        onChange={set("timestampPath")}
        placeholder="webhookTimestamp"
        help="For a sender that puts the timestamp in the body, in seconds or milliseconds."
      />
      {timestampSource(form) && <ToleranceField form={form} set={set} />}
      <ConfigSelect
        label="Signed content"
        value={template ? TEMPLATE : form.signed}
        onChange={(v) => set("signed")(v === TEMPLATE ? "{timestamp}.{body}" : v)}
        options={SIGNED_CONTENT}
      />
      {template && (
        <ConfigField
          label="Signed template"
          required
          mono
          value={form.signed}
          onChange={set("signed")}
          placeholder="v0:{timestamp}:{body}"
          help="The signed bytes over {id}, {timestamp} and {body}, with the text between them."
        />
      )}
      {(form.signed === "id.timestamp.body" || form.signed.includes("{id}")) && (
        <ConfigField
          label="ID header"
          required
          mono
          value={form.idHeader}
          onChange={set("idHeader")}
          placeholder="webhook-id"
        />
      )}
    </>
  );
}

function ToleranceField({ form, set }: { form: WebhookForm; set: Setter }) {
  return (
    <ConfigField
      label="Tolerance (seconds)"
      type="number"
      value={form.toleranceSeconds}
      onChange={set("toleranceSeconds")}
      placeholder="300"
    />
  );
}

/** TEMPLATE is the signed-content choice for a template rather than a named
 * form. */
const TEMPLATE = "template";

/** SIGNED_CONTENT are the bytes a signature can cover, named as the
 * connection editor names them. */
const SIGNED_CONTENT = [
  { value: "body", label: "Body" },
  { value: "timestamp.body", label: "Timestamp and body" },
  { value: "id.timestamp.body", label: "ID, timestamp and body" },
  { value: TEMPLATE, label: "Template" },
];

