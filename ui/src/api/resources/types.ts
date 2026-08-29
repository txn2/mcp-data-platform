export interface Resource {
  id: string;
  scope: "global" | "persona" | "user";
  scope_id: string;
  /**
   * The slash-separated folder path this resource is filed under inside its
   * library, and the tail of its URI ahead of the filename (#1529). A
   * one-segment path is what every resource carried before folders existed.
   */
  path: string;
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
  // last_read_at is when this resource's content was last served through any
  // surface. Absent means never read since the deployment began auditing reads.
  last_read_at?: string;
  // usage is present only on the detail read, and only when audit is enabled.
  usage?: ResourceUsage;
}

// ResourceUsage is the audit-derived read activity of a resource. Both counts
// are bounded by the deployment's audit retention window.
export interface ResourceUsage {
  reads_30d: number;
  reads_90d: number;
  by_surface_30d?: Record<string, number>;
  last_read_at?: string;
}

// ResourceVersion is one recorded content revision. Filename is absent because
// every version of a resource shares the resource's filename: revising content
// keeps the URI, and the URI embeds the filename.
export interface ResourceVersion {
  resource_id: string;
  version: number;
  mime_type: string;
  size_bytes: number;
  s3_key: string;
  uploader_sub: string;
  uploader_email: string;
  restored_from?: number;
  // change_summary says why the content changed, for a revision the platform
  // wrote on the uploader's behalf. Absent for a revision somebody uploaded.
  change_summary?: string;
  created_at: string;
}

export interface ResourceVersionListResponse {
  versions: ResourceVersion[];
  // current is the version number the resource head currently serves.
  current: number;
  max_versions: number;
}

export interface ResourceListResponse {
  resources: Resource[];
  total: number;
}

export interface ResourceUpdate {
  display_name?: string;
  description?: string;
  tags?: string[];
  // path refiles the resource in another folder of its library. Like scope it is
  // not metadata: the folder path is half of the resource's URI, so the server
  // rewrites the address and records the one it vacated (#1528).
  path?: string;
  // scope and scope_id move the resource to another library (#1502). They are
  // sent together, and only when the library is actually changing: the server
  // rewrites the canonical URI on a move and records the old one as an alias, so
  // this is not a field to echo back unchanged.
  scope?: "global" | "persona" | "user";
  scope_id?: string;
}

// FolderMoveRequest renames a folder, or nests it under another one, by
// rewriting the path prefix of every resource beneath it. The library is named
// explicitly because a path is only unique inside one.
export interface FolderMoveRequest {
  scope: "global" | "persona" | "user";
  scope_id?: string;
  from: string;
  to: string;
}

// FolderMoveEntry is one resource a folder move carried.
export interface FolderMoveEntry {
  id: string;
  path: string;
  uri: string;
  from_uri: string;
}

// FolderMoveResult is what a completed folder move reports. The whole move is
// one transaction, so a result means every entry moved.
export interface FolderMoveResult {
  from: string;
  to: string;
  moved: FolderMoveEntry[];
}
