// Endpoint admin types (#2208). They mirror GET /api/v1/admin/endpoints and
// GET /api/v1/admin/endpoints/{name} (internal/admin/endpointapi).

export type EndpointRange = "day" | "week";

export interface EndpointStatusClasses {
  "2xx": number;
  "3xx": number;
  "4xx": number;
  "5xx": number;
}

// EndpointTraffic is what an endpoint answered over the range.
export interface EndpointTraffic {
  requests: number;
  by_status_class: EndpointStatusClasses;
  failures: number;
  p50_ms: number;
  p95_ms: number;
  // callers is how many distinct signed-in callers sent requests.
  callers: number;
  last_request_at?: string;
}

// AdminEndpoint is one binding with its script, owner and traffic.
export interface AdminEndpoint {
  id: string;
  name: string;
  url: string;
  methods: string[];
  path: string;
  auth_mode: "public" | "signed_in";
  open_reads: boolean;
  enabled: boolean;
  timeout_seconds: number;
  max_body_bytes: number;
  rate_limit: number;
  caller_rate_limit: number;
  updated_by: string;
  updated_at: string;
  script: { id: string; name: string; display_name?: string; owner: string };
  traffic: EndpointTraffic;
  // refused counts the requests turned away before the handler ran, and
  // refused_by_reason the same by reason.
  refused: number;
  refused_by_reason: Record<string, number>;
}

// AdminEndpointList is one page of the endpoints the filters match; total is
// how many match in all (#2251).
export interface AdminEndpointList {
  range: EndpointRange;
  since: string;
  endpoints: AdminEndpoint[];
  total: number;
  page: number;
  per_page: number;
}

// AdminEndpointListParams narrows and pages the list on the server.
export interface AdminEndpointListParams {
  range: EndpointRange;
  access?: "" | "public" | "signed_in";
  status?: "" | "on" | "off";
  page?: number;
  perPage?: number;
}

export interface EndpointBucket extends EndpointStatusClasses {
  start: string;
  refused: number;
}

export interface AdminEndpointFailure {
  id: string;
  version: number;
  method: string;
  status: number;
  error: string;
  duration_ms: number;
  created_at: string;
}

export interface AdminEndpointDetail extends AdminEndpoint {
  range: EndpointRange;
  since: string;
  bucket_seconds: number;
  series: EndpointBucket[];
  top_callers: { caller: string; requests: number }[];
  failures: AdminEndpointFailure[];
}
