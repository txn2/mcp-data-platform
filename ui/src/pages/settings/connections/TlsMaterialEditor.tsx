import { useId } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Textarea } from "@/components/ui/textarea";
import { ConfigGroup, update, type ConfigFormProps } from "./fields";

// TLSMaterialEditor renders the per-connection mTLS material section:
// client cert + private key (both required together) and an optional
// CA bundle for upstreams behind a private root. The fields are
// optional for every auth mode and required when auth_mode is "mtls"
// (the cert itself is the credential). PEM blocks are pasted into
// textareas rather than file-picked because operators commonly
// receive the material as text from their PKI tooling and the
// uniform paste path keeps the read-after-save flow trivial: the
// server returns the cert verbatim and the private key as
// [REDACTED], and we surface the leaf certificate's expiry from a
// server-computed field so the badge does not duplicate the parse
// logic in JavaScript.
export function TLSMaterialEditor({
  config,
  onChange,
  onOpenHelp,
}: ConfigFormProps & { onOpenHelp: () => void }) {
  const expiry = String(config.mtls_cert_not_after ?? "");
  const isMTLSMode = config.auth_mode === "mtls";
  return (
    <ConfigGroup
      title={
        <span className="flex items-center gap-2">
          TLS / mTLS
          {isMTLSMode && (
            <Badge variant="info">required for auth_mode: mtls</Badge>
          )}
        </span>
      }
      action={
        <Button type="button" variant="link" size="xs" onClick={onOpenHelp}>
          Learn about TLS / mTLS
        </Button>
      }
    >
      <PEMTextarea
        label="Client certificate (PEM)"
        value={String(config.mtls_client_cert_pem ?? "")}
        onChange={(v) => onChange(update(config, "mtls_client_cert_pem", v))}
        placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
      />
      <PEMTextarea
        label="Client private key (PEM)"
        help="Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
        value={String(config.mtls_client_key_pem ?? "")}
        onChange={(v) => onChange(update(config, "mtls_client_key_pem", v))}
        placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
        sensitive
      />
      <PEMTextarea
        label="CA bundle (PEM)"
        value={String(config.tls_ca_bundle_pem ?? "")}
        onChange={(v) => onChange(update(config, "tls_ca_bundle_pem", v))}
        placeholder="-----BEGIN CERTIFICATE-----&#10;...&#10;-----END CERTIFICATE-----"
      />
      {expiry && <CertExpiryBadge notAfter={expiry} />}
    </ConfigGroup>
  );
}

// PEMTextarea is a multi-line variant of ConfigField for PEM-encoded
// material. Kept local to this file because no other connection kind
// pastes multi-line secrets today; if a second consumer appears, lift
// to a shared component alongside ConfigField.
function PEMTextarea({
  label,
  help,
  value,
  onChange,
  placeholder,
  sensitive,
}: {
  label: string;
  help?: string;
  value: string;
  onChange: (v: string) => void;
  placeholder?: string;
  sensitive?: boolean;
}) {
  const id = useId();
  const helpID = `${id}-help`;
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
        autoComplete={sensitive ? "off" : undefined}
        spellCheck={false}
        rows={5}
        aria-describedby={help ? helpID : undefined}
        // A PEM block needs its five rows before anything is pasted, so the
        // fixed sizing wins over ui/textarea's content sizing.
        className="field-sizing-fixed font-mono text-xs"
      />
      {help && (
        <p id={helpID} className="text-xs text-muted-foreground">
          {help}
        </p>
      )}
    </div>
  );
}

// CertExpiryBadge renders a one-line summary of the client cert's
// NotAfter, variant-coded by remaining time. Treats every input as a
// server-formatted RFC3339 string (the admin handler computes this
// server-side via crypto/x509). A parse failure renders nothing
// rather than guessing.
function CertExpiryBadge({ notAfter }: { notAfter: string }) {
  const ms = Date.parse(notAfter);
  if (Number.isNaN(ms)) return null;
  const days = Math.floor((ms - Date.now()) / (24 * 60 * 60 * 1000));
  if (days < 0) {
    return (
      <Badge variant="danger">
        Certificate expired {-days} day{-days === 1 ? "" : "s"} ago
      </Badge>
    );
  }
  if (days < 30) {
    return (
      <Badge variant="warning">
        Certificate expires in {days} day{days === 1 ? "" : "s"}
      </Badge>
    );
  }
  return <Badge variant="success">Certificate valid for {days} more days</Badge>;
}
