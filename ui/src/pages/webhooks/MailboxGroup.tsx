import { useId } from "react";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  ConfigToggle,
  PEMTextarea,
} from "@/pages/settings/connections/fields";
import type { IMAPForm } from "./imapForm";
import type { WebhookForm } from "./webhookForm";

// MailboxGroup is an imap source's settings (#2247): the mail server, the
// mailbox's login, the folders read and whose mail becomes events. It takes
// the place of the authentication and events groups an http source has.

type Setter = <K extends keyof WebhookForm>(key: K) => (value: WebhookForm[K]) => void;

export function MailboxGroup({ form, creating, set }: { form: WebhookForm; creating: boolean; set: Setter }) {
  const m = form.imap;
  const setIMAP = <K extends keyof IMAPForm>(key: K) => (value: IMAPForm[K]) =>
    set("imap")({ ...m, [key]: value });
  return (
    <>
      <ConfigGroup title="Mail server">
        <div className="grid gap-3 sm:grid-cols-3">
          <ConfigField label="Host" required mono value={m.host} onChange={setIMAP("host")} placeholder="imap.example.com" />
          <ConfigSelect
            label="Encryption"
            value={m.tls}
            onChange={(v) => setIMAP("tls")(v as IMAPForm["tls"])}
            options={[
              { value: "tls", label: "TLS (IMAPS)" },
              { value: "starttls", label: "STARTTLS" },
            ]}
          />
          <ConfigField
            label="Port"
            type="number"
            value={m.port}
            onChange={setIMAP("port")}
            placeholder={m.tls === "starttls" ? "143" : "993"}
          />
        </div>
        <div className="grid gap-3 sm:grid-cols-2">
          <ConfigField label="Username" required value={form.username} onChange={set("username")} />
          <ConfigField
            label={creating ? "Password" : "New password"}
            required={creating}
            sensitive
            value={form.secret}
            onChange={set("secret")}
            help={creating ? "Never shown again after it is saved." : "Leave empty to keep the stored password."}
          />
        </div>
        <PEMTextarea
          label="Certificate authorities to trust"
          value={m.caBundle}
          onChange={setIMAP("caBundle")}
          placeholder="-----BEGIN CERTIFICATE-----"
          help="Only for a server whose certificate a private CA signed. Empty trusts the system's authorities."
        />
        <p className="text-xs text-muted-foreground">
          The platform signs in, opens each folder read-only and never marks, moves or deletes a message. The
          mailbox is checked when the source is saved.
        </p>
      </ConfigGroup>

      <ConfigGroup title="What is read">
        <ListField
          label="Folders"
          value={m.folders}
          onChange={setIMAP("folders")}
          placeholder="INBOX"
          help="One per line. Each keeps its own place, so a message is read once."
        />
        <ListField
          label="Allowed senders"
          value={m.allowSenders}
          onChange={setIMAP("allowSenders")}
          placeholder={"no-reply@zoom.us\n@example.com"}
          help="One address or @domain per line. Mail from anyone else is counted and never stored."
        />
        <ConfigToggle
          label="Require an authenticated sender"
          help="Keep an allowed sender's mail only when the receiving server recorded an SPF, DKIM or DMARC pass for the From domain."
          checked={m.requireAuthPass}
          onChange={setIMAP("requireAuthPass")}
        />
        {m.requireAuthPass && (
          <ConfigField
            label="Receiving server id"
            required
            mono
            value={m.trustedAuthServID}
            onChange={setIMAP("trustedAuthServID")}
            placeholder="mx.example.com"
            help="The id the receiving server writes at the start of its Authentication-Results header. Only that header is read; a sender can write one too."
          />
        )}
        <div className="grid gap-3 sm:grid-cols-3">
          <ConfigField label="Check every (seconds)" type="number" value={m.pollSeconds} onChange={setIMAP("pollSeconds")} placeholder="60" />
          <ConfigField
            label="Largest message (bytes)"
            type="number"
            value={m.maxMessageBytes}
            onChange={setIMAP("maxMessageBytes")}
            placeholder="10485760"
            help="A larger message is filed with its headers and no body."
          />
          <ConfigField
            label="Largest attachment (bytes)"
            type="number"
            value={m.maxAttachmentBytes}
            onChange={setIMAP("maxAttachmentBytes")}
            placeholder="26214400"
          />
        </div>
      </ConfigGroup>
    </>
  );
}

function ListField({
  label,
  value,
  onChange,
  placeholder,
  help,
}: {
  label: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  help: string;
}) {
  const id = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={id} className="text-xs">
        {label}
      </Label>
      <Textarea
        id={id}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        spellCheck={false}
        rows={3}
        aria-describedby={`${id}-help`}
        className="font-mono text-xs"
      />
      <p id={`${id}-help`} className="text-xs text-muted-foreground">
        {help}
      </p>
    </div>
  );
}
