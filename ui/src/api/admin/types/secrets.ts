// Stored secrets (#2051): a value a request references as {{secret:<name>}},
// filled in by the api gateway as it sends the request. The value is
// write-only, so none of these carry it back.

export interface Secret {
  name: string;
  description: string;
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
  value?: string;
  allow_connections: string[];
  allow_personas: string[];
}
