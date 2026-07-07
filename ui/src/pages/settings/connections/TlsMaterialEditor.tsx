import { cn } from "@/lib/utils";
import { update, type ConfigFormProps } from "./fields";

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
    <div className="rounded-md border bg-muted/20 px-3 py-3 space-y-3">
      <div className="flex items-center justify-between">
        <div className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
          TLS / mTLS
          {isMTLSMode && (
            <span className="ml-2 rounded bg-blue-100 px-1.5 py-0.5 text-[10px] font-medium text-blue-700 dark:bg-blue-900/40 dark:text-blue-200">
              required for auth_mode: mtls
            </span>
          )}
        </div>
        <button
          type="button"
          onClick={onOpenHelp}
          className="text-xs text-blue-600 hover:underline dark:text-blue-400"
        >
          Learn about TLS / mTLS
        </button>
      </div>
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
    </div>
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
  return (
    <div>
      <label className="mb-1 block text-xs font-medium">{label}</label>
      <textarea
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        autoComplete={sensitive ? "off" : undefined}
        spellCheck={false}
        rows={5}
        className="w-full rounded-md border bg-background px-3 py-2 text-xs font-mono outline-none ring-ring focus:ring-2"
      />
      {help && <p className="mt-1 text-xs text-muted-foreground">{help}</p>}
    </div>
  );
}

// CertExpiryBadge renders a one-line summary of the client cert's
// NotAfter, color-coded by remaining time. Treats every input as a
// server-formatted RFC3339 string (the admin handler computes this
// server-side via crypto/x509). A parse failure renders nothing
// rather than guessing.
function CertExpiryBadge({ notAfter }: { notAfter: string }) {
  const ms = Date.parse(notAfter);
  if (Number.isNaN(ms)) return null;
  const now = Date.now();
  const days = Math.floor((ms - now) / (24 * 60 * 60 * 1000));
  let label: string;
  let tone: string;
  if (days < 0) {
    label = `Certificate expired ${-days} day${-days === 1 ? "" : "s"} ago`;
    tone = "bg-red-100 text-red-700 dark:bg-red-900/40 dark:text-red-200";
  } else if (days < 30) {
    label = `Certificate expires in ${days} day${days === 1 ? "" : "s"}`;
    tone = "bg-amber-100 text-amber-800 dark:bg-amber-900/40 dark:text-amber-200";
  } else {
    label = `Certificate valid for ${days} more days`;
    tone = "bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200";
  }
  return (
    <div className={cn("inline-flex rounded px-2 py-1 text-xs font-medium", tone)}>
      {label}
    </div>
  );
}
