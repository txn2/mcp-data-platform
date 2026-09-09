import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  PEMTextarea,
  update,
  type ConfigFormProps,
} from "./fields";

// The auth_mode=signed_jwt block of the api and graphql connection
// editors. The platform mints the token itself from an identifier and a
// signing key the operator was issued out of band, so unlike the OAuth
// block there is no endpoint to reach and no browser flow to complete:
// every field here is a value the upstream registered and checks
// literally. Split into its own file for the same reason the OAuth
// block was — the mode carries enough fields to read as its own form.

const ALGORITHMS = [
  { value: "HS256", label: "HS256 — shared secret (HMAC)" },
  { value: "RS256", label: "RS256 — RSA private key" },
  { value: "ES256", label: "ES256 — ECDSA P-256 private key" },
];

// KeyMaterial is the half of the form the algorithm selects: a shared
// secret for HS256, a PEM signing key and its optional id otherwise.
function KeyMaterial({ config, onChange }: ConfigFormProps) {
  const algorithm = String(config.jwt_algorithm ?? "HS256");
  if (algorithm === "HS256") {
    return (
      <ConfigField
        label="Client secret"
        help="The shared secret the upstream issued alongside the client id. Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
        value={String(config.jwt_client_secret ?? "")}
        onChange={(v) => onChange(update(config, "jwt_client_secret", v))}
        sensitive
      />
    );
  }
  return (
    <>
      <PEMTextarea
        label="Signing key (PEM)"
        help="The private key the upstream registered. Encrypted at rest. Use [REDACTED] to keep the existing value when re-saving."
        value={String(config.jwt_private_key_pem ?? "")}
        onChange={(v) => onChange(update(config, "jwt_private_key_pem", v))}
        placeholder="-----BEGIN PRIVATE KEY-----&#10;...&#10;-----END PRIVATE KEY-----"
        sensitive
      />
      <ConfigField
        label="Key ID"
        help="Sent as the token's kid header. Set it when the upstream holds several registered keys for this account; leave empty otherwise."
        value={String(config.jwt_key_id ?? "")}
        onChange={(v) => onChange(update(config, "jwt_key_id", v))}
        placeholder="ABC1234567"
        mono
      />
    </>
  );
}

// SignedJWTAuthFields renders the whole signed_jwt block: the algorithm
// and the key material it selects, the three claims the upstream
// matches, and the two timings.
export function SignedJWTAuthFields({ config, onChange }: ConfigFormProps) {
  return (
    <ConfigGroup title="Signed JWT">
      <ConfigSelect
        label="Algorithm"
        value={String(config.jwt_algorithm ?? "HS256")}
        onChange={(v) => onChange(update(config, "jwt_algorithm", v))}
        options={ALGORITHMS}
      />
      <KeyMaterial config={config} onChange={onChange} />
      <ConfigField
        label="Issuer (iss)"
        help="The client id the upstream issued. Leave empty to omit the claim; one of issuer or subject must be set."
        value={String(config.jwt_issuer ?? "")}
        onChange={(v) => onChange(update(config, "jwt_issuer", v))}
        placeholder="CLIENTID-2f7c"
        mono
      />
      <ConfigField
        label="Subject (sub)"
        help="The user or service the upstream authorized for this application. Leave empty to omit the claim — an upstream that registered no subject refuses a token that carries one."
        value={String(config.jwt_subject ?? "")}
        onChange={(v) => onChange(update(config, "jwt_subject", v))}
        placeholder="svc-integration"
        mono
      />
      <ConfigField
        label="Audience (aud)"
        help="Defaults to this connection's endpoint URL. It must match what the upstream registered, byte for byte — a trailing slash or a different host name is a rejected token."
        value={String(config.jwt_audience ?? "")}
        onChange={(v) => onChange(update(config, "jwt_audience", v))}
        placeholder="(the connection's endpoint URL)"
        mono
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Token lifetime"
          help="exp - iat. Defaults to 300s. Must not exceed the lifetime the upstream registered."
          value={String(config.jwt_token_lifetime ?? "")}
          onChange={(v) => onChange(update(config, "jwt_token_lifetime", v))}
          placeholder="300s"
          mono
        />
        <ConfigField
          label="Issued-at skew"
          help="How far back iat is set to absorb clock drift, and how early a cached token is replaced. Defaults to 30s; must be under the lifetime."
          value={String(config.jwt_issued_at_skew ?? "")}
          onChange={(v) => onChange(update(config, "jwt_issued_at_skew", v))}
          placeholder="30s"
          mono
        />
      </div>
    </ConfigGroup>
  );
}
