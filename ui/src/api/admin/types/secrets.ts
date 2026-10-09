// Stored secrets (#2051): a value a request references as {{secret:<name>}},
// filled in by the api gateway as it sends the request. The value is
// write-only, so none of these carry it back. A secret of kind totp holds an
// authenticator seed instead, and a request references its current one-time
// code as {{totp:<name>}} (#2065).

export type SecretKind = "value" | "totp";

/** TOTPParams are what a totp secret's codes are computed with. */
export interface TOTPParams {
  algorithm: string;
  digits: number;
  period: number;
}

export interface Secret {
  name: string;
  description: string;
  kind: SecretKind;
  totp?: TOTPParams;
  allow_connections: string[];
  allow_personas: string[];
  created_by: string;
  updated_by: string;
  created_at: string;
  updated_at: string;
}

export interface SecretList {
  secrets: Secret[];
}

/** SecretInput is a create or a change; value is omitted to keep the stored one. */
export interface SecretInput {
  description: string;
  kind?: SecretKind;
  value?: string;
  allow_connections: string[];
  allow_personas: string[];
}

/** CurrentCode is a totp secret's code for this moment; reading it issues nothing. */
export interface CurrentCode {
  code: string;
  seconds_left: number;
  totp: TOTPParams;
}
