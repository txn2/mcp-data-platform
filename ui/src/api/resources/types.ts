export interface Resource {
  id: string;
  scope: "global" | "persona" | "user";
  scope_id: string;
  category: string;
  filename: string;
  display_name: string;
  description: string;
  mime_type: string;
  size_bytes: number;
  s3_key: string;
  uri: string;
  tags: string[];
  uploader_sub: string;
  uploader_email: string;
  created_at: string;
  updated_at: string;
}

export interface ResourceListResponse {
  resources: Resource[];
  total: number;
}

export interface ResourceUpdate {
  display_name?: string;
  description?: string;
  tags?: string[];
  category?: string;
}
