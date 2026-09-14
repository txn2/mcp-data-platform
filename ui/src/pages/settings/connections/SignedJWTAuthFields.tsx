import {
  ConfigField,
  ConfigGroup,
  ConfigSelect,
  PEMTextarea,
  update,
  type ConfigFormProps,
} from "./fields";

// The assertion block of the api and graphql connection editors, rendered
// for two configurations that sign the same claims with the same keys.
//
// Under auth_mode=signed_jwt the platform mints the token itself and
// presents it as the bearer credential, so there is no endpoint to reach
// and no browser flow to complete. Under oauth_grant=jwt_bearer (RFC 7523)
// the OAuth block renders it, and the assertion is exchanged at the token
// endpoint for an access token. Every field is a value the upstream
// registered and checks literally; what differs between the two is which
// defaults the server applies and which claims it requires, which is what
// VARIANTS states.

// AssertionVariant names the configuration the block is rendered for.
export type AssertionVariant = "signed_jwt" | "jwt_bearer";

const VARIANTS: Record<
  AssertionVariant,
  {
    title: string;
    defaultAlgorithm: string;
    issuerHelp: string;
    subjectHelp: string;
    audienceHelp: string;
    audiencePlaceholder: string;
    lifetimeHelp: string;
    skewHelp: string;
  }
> = {
  signed_jwt: {
    title: "Signed JWT",
    defaultAlgorithm: "HS256",
    issuerHelp:
      "The client id the upstream issued. Leave empty to omit the claim; one of issuer or subject must be set.",
    subjectHelp:
      "The user or service the upstream authorized for this application. Leave empty to omit the claim — an upstream that registered no subject refuses a token that carries one.",
    audienceHelp:
      "Defaults to this connection's endpoint URL. It must match what the upstream registered, byte for byte — a trailing slash or a different host name is a rejected token.",
    audiencePlaceholder: "(the connection's endpoint URL)",
    lifetimeHelp:
      "exp - iat. Defaults to 300s. Must not exceed the lifetime the upstream registered.",
    skewHelp:
      "How far back iat is set to absorb clock drift, and how early a cached token is replaced. Defaults to 30s; must be under the lifetime.",
  },
  jwt_bearer: {
    title: "Signed assertion (RFC 7523)",
    defaultAlgorithm: "RS256",
    issuerHelp:
      "Required. Usually the client id (consumer key) of the application the upstream registered the key under.",
    subjectHelp:
      "Required. The integration user the access token acts as. The upstream must have approved this user for the application.",
    audienceHelp:
      "Defaults to the token URL. Some upstreams register a different value (a login host rather than the token path); it must match byte for byte.",
    audiencePlaceholder: "(the token URL)",
    lifetimeHelp:
      "exp - iat of the assertion, not of the access token. Defaults to 300s; upstreams commonly refuse more than a few minutes.",
    skewHelp:
      "How far back iat is set to absorb clock drift. Defaults to 30s; must be under the lifetime.",
  },
};

const ALGORITHMS = [
  { value: "HS256", label: "HS256 — shared secret (HMAC)" },
  { value: "RS256", label: "RS256 — RSA private key" },
  { value: "ES256", label: "ES256 — ECDSA P-256 private key" },
];

// KeyMaterial is the half of the form the algorithm selects: a shared
// secret for HS256, a PEM signing key and its optional id otherwise.
function KeyMaterial({
  config,
  onChange,
  algorithm,
}: ConfigFormProps & { algorithm: string }) {
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

// SignedJWTAuthFields renders the whole assertion block: the algorithm
// and the key material it selects, the three claims the upstream
// matches, and the two timings.
export function SignedJWTAuthFields({
  config,
  onChange,
  variant = "signed_jwt",
}: ConfigFormProps & { variant?: AssertionVariant }) {
  const copy = VARIANTS[variant];
  const algorithm = String(config.jwt_algorithm || copy.defaultAlgorithm);
  return (
    <ConfigGroup title={copy.title}>
      <ConfigSelect
        label="Algorithm"
        value={algorithm}
        onChange={(v) => onChange(update(config, "jwt_algorithm", v))}
        options={ALGORITHMS}
      />
      <KeyMaterial config={config} onChange={onChange} algorithm={algorithm} />
      <ConfigField
        label="Issuer (iss)"
        help={copy.issuerHelp}
        value={String(config.jwt_issuer ?? "")}
        onChange={(v) => onChange(update(config, "jwt_issuer", v))}
        placeholder="CLIENTID-2f7c"
        mono
      />
      <ConfigField
        label="Subject (sub)"
        help={copy.subjectHelp}
        value={String(config.jwt_subject ?? "")}
        onChange={(v) => onChange(update(config, "jwt_subject", v))}
        placeholder="svc-integration"
        mono
      />
      <ConfigField
        label="Audience (aud)"
        help={copy.audienceHelp}
        value={String(config.jwt_audience ?? "")}
        onChange={(v) => onChange(update(config, "jwt_audience", v))}
        placeholder={copy.audiencePlaceholder}
        mono
      />
      <div className="grid grid-cols-2 gap-3">
        <ConfigField
          label="Token lifetime"
          help={copy.lifetimeHelp}
          value={String(config.jwt_token_lifetime ?? "")}
          onChange={(v) => onChange(update(config, "jwt_token_lifetime", v))}
          placeholder="300s"
          mono
        />
        <ConfigField
          label="Issued-at skew"
          help={copy.skewHelp}
          value={String(config.jwt_issued_at_skew ?? "")}
          onChange={(v) => onChange(update(config, "jwt_issued_at_skew", v))}
          placeholder="30s"
          mono
        />
      </div>
    </ConfigGroup>
  );
}
