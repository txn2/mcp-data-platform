export interface Asset {
  id: string;
  owner_id: string;
  name: string;
  description: string;
  content_type: string;
  s3_bucket: string;
  s3_key: string;
  size_bytes: number;
  tags: string[];
  provenance: Provenance;
  session_id: string;
  created_at: string;
  updated_at: string;
  deleted_at?: string;
}

export interface Provenance {
  tool_calls?: ProvenanceToolCall[];
  session_id?: string;
  user_id?: string;
}

export interface ProvenanceToolCall {
  tool_name: string;
  timestamp: string;
  summary?: string;
}

export interface Share {
  id: string;
  asset_id: string;
  token: string;
  created_by: string;
  shared_with_user_id?: string;
  shared_with_email?: string;
  expires_at?: string;
  revoked: boolean;
  access_count: number;
  last_accessed_at?: string;
  created_at: string;
}

export interface SharedAsset {
  asset: Asset;
  share_id: string;
  shared_by: string;
  shared_at: string;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  limit: number;
  offset: number;
}

export interface ShareResponse {
  share: Share;
  share_url?: string;
}

export interface Branding {
  name: string;
  portal_title: string;
  portal_logo: string;
  portal_logo_light: string;
  portal_logo_dark: string;
}
