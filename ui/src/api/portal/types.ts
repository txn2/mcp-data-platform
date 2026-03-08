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

export interface ShareSummary {
  has_user_share: boolean;
  has_public_link: boolean;
}

export interface PaginatedResponse<T> {
  data: T[];
  total: number;
  limit: number;
  offset: number;
  share_summaries?: Record<string, ShareSummary>;
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

// Activity types (user-scoped audit metrics)
export interface ActivityOverview {
  total_calls: number;
  success_rate: number;
  avg_duration_ms: number;
  unique_users: number;
  unique_tools: number;
  enrichment_rate: number;
  error_count: number;
}

export interface TimeseriesBucket {
  bucket: string;
  count: number;
  success_count: number;
  error_count: number;
  avg_duration_ms: number;
}

export interface BreakdownEntry {
  dimension: string;
  count: number;
  success_rate: number;
  avg_duration_ms: number;
}

// Knowledge types (user-scoped insights)
export interface Insight {
  id: string;
  created_at: string;
  session_id: string;
  captured_by: string;
  persona: string;
  source: string;
  category: string;
  insight_text: string;
  confidence: string;
  entity_urns: string[];
  related_columns: RelatedColumn[];
  suggested_actions: SuggestedAction[];
  status: string;
  reviewed_by?: string;
  reviewed_at?: string;
  review_notes?: string;
  applied_by?: string;
  applied_at?: string;
  changeset_ref?: string;
}

export interface RelatedColumn {
  urn: string;
  column: string;
  relevance: string;
}

export interface SuggestedAction {
  action_type: string;
  target: string;
  detail: string;
  query_sql?: string;
  query_description?: string;
}

export interface InsightStats {
  total_pending: number;
  by_status: Record<string, number>;
  by_category: Record<string, number>;
  by_confidence: Record<string, number>;
}
