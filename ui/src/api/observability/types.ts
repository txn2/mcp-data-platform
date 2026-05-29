// Prometheus HTTP API result types. The platform's PromQL proxy
// (/api/v1/observability/query and /query_range) returns Prometheus's
// native JSON unchanged, so these mirror the Prometheus query API
// response shape rather than any platform-specific type.

// PromSample is one instant sample: [unixSeconds, "value"]. Prometheus
// encodes the sample value as a string.
export type PromSample = [number, string];

// PromVectorResult is one series in an instant (vector) query result.
export interface PromVectorResult {
  metric: Record<string, string>;
  value: PromSample;
}

// PromMatrixResult is one series in a range (matrix) query result.
export interface PromMatrixResult {
  metric: Record<string, string>;
  values: PromSample[];
}

// PromVectorResponse is the response of an instant query (resultType
// "vector"). Scalar/string result types are not used by this UI.
export interface PromVectorResponse {
  status: "success" | "error";
  data: {
    resultType: "vector";
    result: PromVectorResult[];
  };
}

// PromMatrixResponse is the response of a range query (resultType
// "matrix").
export interface PromMatrixResponse {
  status: "success" | "error";
  data: {
    resultType: "matrix";
    result: PromMatrixResult[];
  };
}
