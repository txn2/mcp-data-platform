import { useId } from "react";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ConfigField, ConfigGroup, ConfigSelect, ConfigToggle, PEMTextarea, update, type ConfigFormProps } from "./fields";

const TLS_MODES = [
  { value: "tls", label: "IMAPS (port 993, default)" },
  { value: "starttls", label: "STARTTLS (port 143)" },
];

/** A list key as the editor shows it: one entry per line. */
export function listText(raw: unknown): string {
  if (Array.isArray(raw)) return raw.map(String).join("\n");
  if (typeof raw === "string") return raw.split(",").map((s) => s.trim()).filter(Boolean).join("\n");
  return "";
}

/** The entries of a one-per-line list, blanks dropped; undefined when empty. */
export function listValue(text: string): string[] | undefined {
  const out = text.split(/[\n,]/).map((s) => s.trim()).filter(Boolean);
  return out.length > 0 ? out : undefined;
}

function ListField({
  label,
  help,
  value,
  onChange,
  placeholder,
}: {
  label: string;
  help: string;
  value: unknown;
  onChange: (v: string[] | undefined) => void;
  placeholder: string;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="gap-0.5 text-xs">
        {label}
        <span className="text-destructive">*</span>
      </Label>
      <Textarea
        id={id}
        value={listText(value)}
        onChange={(e) => onChange(listValue(e.target.value))}
        placeholder={placeholder}
        spellCheck={false}
        rows={3}
        aria-describedby={`${id}-help`}
        className="field-sizing-fixed font-mono text-xs"
      />
      <p id={`${id}-help`} className="text-xs text-muted-foreground">
        {help}
      </p>
    </div>
  );
}

/** A whole-number key, blank meaning the default. */
function NumberField({
  config,
  onChange,
  name,
  label,
  placeholder,
}: ConfigFormProps & { name: string; label: string; placeholder: string }) {
  const v = config[name];
  return (
    <ConfigField
      label={label}
      type="number"
      value={v == null ? "" : String(v)}
      onChange={(next) => onChange(update(config, name, next ? Number(next) : undefined))}
      placeholder={placeholder}
    />
  );
}

// IMAPConfigForm renders the editor for kind=imap connections (#2247): a
// dedicated platform mailbox, the folders it may read, and whose mail is
// shown. Mail from anyone else is never returned by any tool, only counted.
export function IMAPConfigForm({ config, onChange }: ConfigFormProps) {
  const requireAuth = config.require_auth_pass !== false;
  return (
    <>
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <div className="sm:col-span-2">
          <ConfigField
            label="Host"
            value={String(config.host ?? "")}
            onChange={(v) => onChange(update(config, "host", v))}
            placeholder="imap.example.com"
            mono
            required
          />
        </div>
        <NumberField config={config} onChange={onChange} name="port" label="Port" placeholder={config.tls === "starttls" ? "143" : "993"} />
      </div>
      <ConfigSelect
        label="TLS"
        help="Plain IMAP is not offered: the password would cross the network in the clear."
        value={String(config.tls ?? "tls")}
        onChange={(v) => onChange(update(config, "tls", v))}
        options={TLS_MODES}
      />
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
        <ConfigField
          label="Username"
          value={String(config.username ?? "")}
          onChange={(v) => onChange(update(config, "username", v))}
          placeholder="platform@example.com"
          mono
          required
        />
        <ConfigField
          label="Password"
          value={String(config.password ?? "")}
          onChange={(v) => onChange(update(config, "password", v))}
          placeholder="••••••••"
          sensitive
          required
          help="An app password. To use a stored secret, write {{secret:<name>}}; it is read each time a session opens."
        />
      </div>
      <ListField
        label="Folders"
        help="The folders this connection may read, one per line. There is no all-folders default: file the mail the platform should see into one folder with a server-side rule."
        value={config.folders}
        onChange={(v) => onChange(update(config, "folders", v))}
        placeholder="Platform"
      />
      <ConfigGroup title="Who the platform reads">
        <ListField
          label="Allowed senders"
          help="Addresses (ap@vendor.example) and domains (@vendor.example), one per line. Mail from anyone else is never shown, only counted."
          value={config.allow_senders}
          onChange={(v) => onChange(update(config, "allow_senders", v))}
          placeholder="@vendor.example"
        />
        <ConfigToggle
          label="Require an authenticated sender"
          checked={requireAuth}
          onChange={(v) => onChange(update(config, "require_auth_pass", v))}
          help="Trust an allowed sender only when the receiving mail server recorded an SPF, DKIM or DMARC pass aligned with the From address. Without it, anyone can claim an allowed address."
        />
        {requireAuth && (
          <ConfigField
            label="Receiving server's id"
            value={String(config.trusted_authserv_id ?? "")}
            onChange={(v) => onChange(update(config, "trusted_authserv_id", v))}
            placeholder="mx.example.com"
            mono
            required
            help="The first word of the Authentication-Results header your mail server adds. Only that header is read, because a sender can write the header too."
          />
        )}
      </ConfigGroup>
      <ConfigGroup title="Limits and TLS">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
          <NumberField config={config} onChange={onChange} name="max_results" label="Max results" placeholder="50" />
          <NumberField config={config} onChange={onChange} name="max_message_bytes" label="Max message bytes" placeholder="10485760" />
          <NumberField config={config} onChange={onChange} name="max_attachment_bytes" label="Max attachment bytes" placeholder="26214400" />
        </div>
        <PEMTextarea
          label="CA bundle"
          value={String(config.tls_ca_bundle_pem ?? "")}
          onChange={(v) => onChange(update(config, "tls_ca_bundle_pem", v || undefined))}
          help="PEM certificates to trust for the mail server, for a private CA."
        />
      </ConfigGroup>
    </>
  );
}
