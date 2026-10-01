import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  update,
  type ConfigFormProps,
} from "./fields";

// The signing block of the api and graphql connection editors under
// auth_mode=hmac (#1996): the platform signs every request the way a webhook
// receiver verifies it. The keys mirror an inbound webhook source's auth
// settings, so a connection and a source given the same values agree.

// HMACPreset is a receiver's whole signing convention. Its values are what
// internal/hmacsig's presets set; a field the operator fills overrides the
// preset's value for it, and an empty field shows the preset's value as its
// placeholder.
interface HMACPreset {
  algorithm: string;
  encoding: string;
  signatureHeader: string;
  prefix: string;
  signed: string;
  headerFormat: string;
  timestampHeader: string;
  idHeader: string;
}

const DEFAULTS: HMACPreset = {
  algorithm: "sha256",
  encoding: "hex",
  signatureHeader: "X-Signature",
  prefix: "",
  signed: "body",
  headerFormat: "",
  timestampHeader: "",
  idHeader: "",
};

export const HMAC_PRESETS: Record<string, HMACPreset> = {
  standard_webhooks: {
    ...DEFAULTS,
    encoding: "base64",
    signatureHeader: "webhook-signature",
    prefix: "v1,",
    signed: "id.timestamp.body",
    timestampHeader: "webhook-timestamp",
    idHeader: "webhook-id",
  },
  github: { ...DEFAULTS, signatureHeader: "X-Hub-Signature-256", prefix: "sha256=" },
  stripe: { ...DEFAULTS, signatureHeader: "Stripe-Signature", signed: "timestamp.body", headerFormat: "stripe" },
  platform: {
    ...DEFAULTS,
    prefix: "sha256=",
    signed: "timestamp.body",
    timestampHeader: "X-Timestamp",
  },
};

const PRESET_OPTIONS = [
  { value: "", label: "None" },
  { value: "standard_webhooks", label: "Standard Webhooks" },
  { value: "github", label: "GitHub" },
  { value: "stripe", label: "Stripe" },
  { value: "platform", label: "Platform" },
];

// An empty value on a select means the preset's value, or the platform's
// default with no preset; the option names it so the operator sees what
// will be sent. The explicit option equal to it is left out, and so is an
// option whose value is itself empty: saving it would save "inherit", which
// over a preset is the preset's value rather than the one it names.
function inherit(source: string, value: string, options: { value: string; label: string }[]) {
  const named = options.find((o) => o.value === value)?.label ?? value;
  return [
    { value: "", label: `${source} (${named})` },
    ...options.filter((o) => o.value !== value && o.value !== ""),
  ];
}

const ALGORITHMS = [
  { value: "sha256", label: "SHA-256" },
  { value: "sha1", label: "SHA-1" },
  { value: "sha512", label: "SHA-512" },
];

const ENCODINGS = [
  { value: "hex", label: "Hex" },
  { value: "base64", label: "Base64" },
];

const SIGNED = [
  { value: "body", label: "Body" },
  { value: "timestamp.body", label: "Timestamp and body" },
  { value: "id.timestamp.body", label: "ID, timestamp and body" },
];

const HEADER_FORMATS = [
  { value: "", label: "Prefix and signature" },
  { value: "stripe", label: "Stripe" },
];

const TIMESTAMP_UNITS = [
  { value: "seconds", label: "Seconds" },
  { value: "milliseconds", label: "Milliseconds" },
];

// str reads a config value as a string.
function str(config: Record<string, unknown>, key: string): string {
  return String(config[key] ?? "");
}

export function HMACAuthFields({ config, onChange }: ConfigFormProps) {
  const preset = HMAC_PRESETS[str(config, "hmac_preset")] ?? DEFAULTS;
  const source = str(config, "hmac_preset") ? "Preset" : "Default";
  const set = (key: string) => (v: string) => onChange(update(config, key, v));
  const stripe = (str(config, "hmac_header_format") || preset.headerFormat) === "stripe";
  return (
    <ConfigGroup title="HMAC signature">
      <ConfigField
        label="Signing secret"
        help="The secret the receiver verifies the signature with. A Standard Webhooks secret starting whsec_ is used as that standard reads it. Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
        value={str(config, "credential")}
        onChange={set("credential")}
        sensitive
      />
      <ConfigSelect
        label="Preset"
        value={str(config, "hmac_preset")}
        onChange={set("hmac_preset")}
        options={PRESET_OPTIONS}
      />
      <SchemeFields config={config} set={set} preset={preset} source={source} />
      {!stripe && <SignedFields config={config} set={set} preset={preset} source={source} />}
      <ConfigField
        label="Delivery ID header"
        help="Carries an id per request: the caller's when a call sets this header, generated otherwise. Required for ID, timestamp and body."
        value={str(config, "hmac_id_header")}
        onChange={set("hmac_id_header")}
        placeholder={preset.idHeader || "(none)"}
        mono
      />
    </ConfigGroup>
  );
}

interface PartProps {
  config: Record<string, unknown>;
  set: (key: string) => (v: string) => void;
  preset: HMACPreset;
  source: string;
}

// SchemeFields are the algorithm, encoding and signature header.
function SchemeFields({ config, set, preset, source }: PartProps) {
  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <ConfigSelect
          label="Algorithm"
          value={str(config, "hmac_algorithm")}
          onChange={set("hmac_algorithm")}
          options={inherit(source, preset.algorithm, ALGORITHMS)}
        />
        <ConfigSelect
          label="Encoding"
          value={str(config, "hmac_encoding")}
          onChange={set("hmac_encoding")}
          options={inherit(source, preset.encoding, ENCODINGS)}
        />
      </div>
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Signature header"
          value={str(config, "hmac_signature_header")}
          onChange={set("hmac_signature_header")}
          placeholder={preset.signatureHeader}
          mono
        />
        <ConfigSelect
          label="Header format"
          value={str(config, "hmac_header_format")}
          onChange={set("hmac_header_format")}
          options={inherit(source, preset.headerFormat, HEADER_FORMATS)}
        />
      </div>
    </>
  );
}

// SignedFields are the prefix, the signed content and the timestamp, which a
// Stripe header format fixes itself.
function SignedFields({ config, set, preset, source }: PartProps) {
  const signed = str(config, "hmac_signed") || preset.signed;
  return (
    <>
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Prefix"
          help="Written before the signature, such as sha256= or v1,"
          value={str(config, "hmac_prefix")}
          onChange={set("hmac_prefix")}
          placeholder={preset.prefix || "(none)"}
          mono
        />
        <ConfigSelect
          label="Signed content"
          value={str(config, "hmac_signed")}
          onChange={set("hmac_signed")}
          options={inherit(source, preset.signed, SIGNED)}
        />
      </div>
      {signed !== "body" && (
        <div className="grid grid-cols-2 gap-3">
          <ConfigField
            label="Timestamp header"
            value={str(config, "hmac_timestamp_header")}
            onChange={set("hmac_timestamp_header")}
            placeholder={preset.timestampHeader || "X-Timestamp"}
            mono
          />
          <ConfigSelect
            label="Timestamp unit"
            value={str(config, "hmac_timestamp_unit") || "seconds"}
            onChange={set("hmac_timestamp_unit")}
            options={TIMESTAMP_UNITS}
          />
        </div>
      )}
    </>
  );
}
