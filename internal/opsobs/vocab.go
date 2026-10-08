package opsobs

// The label values the #1898 series are recorded under. They live here rather
// than beside the instruments in pkg/observability so the vocabulary does not
// grow that package's exported surface; every value is a closed set the
// approved label keys there document.

// Authentication methods auth_attempts_total names.
const (
	AuthMethodOIDC    = "oidc"
	AuthMethodAPIKey  = "api_key"
	AuthMethodOAuth   = "oauth"
	AuthMethodBrowser = "browser"
)

// Authentication results.
const (
	AuthResultSuccess = "success"
	AuthResultFailure = "failure"
)

// Authentication failure reasons. A reason is a class, never the error text:
// the text can carry a token's claims or a key's prefix.
const (
	AuthReasonNone           = "none"
	AuthReasonExpired        = "expired"
	AuthReasonRevoked        = "revoked"
	AuthReasonBadSignature   = "bad_signature"
	AuthReasonUnknownKey     = "unknown_key"
	AuthReasonIDPUnavailable = "idp_unavailable"
	AuthReasonInvalidClaims  = "invalid_claims"
	AuthReasonMalformed      = "malformed"
	// AuthReasonCodeRejected is a browser sign-in whose authorization code the
	// identity provider refused with a 4xx: a code that expired or was already
	// used (a refreshed callback, the back button), or a client the provider
	// does not accept. The provider answered, so it is not idp_unavailable.
	AuthReasonCodeRejected = "code_rejected"
)

// Tool-call denial reasons mcp_tool_call_denials_total records.
const (
	DenialToolDenied       = "tool_denied"
	DenialConnectionDenied = "connection_denied"
	DenialNoPersona        = "no_persona"
)

// Dependencies dependency_up reports.
const (
	DependencyDatabase      = "database"
	DependencySemantic      = "semantic"
	DependencyQuery         = "query"
	DependencyObjectStorage = "object_storage"
	DependencyIDP           = "idp"
	DependencyRenderer      = "renderer"
)

// Connection states mcp_platform_connections reports. A connection whose kind
// has no probe is configured; one that answered its probe is healthy; one whose
// upstream refused the platform's credential is auth_failed; any other failure
// is unreachable.
const (
	ConnectionConfigured  = "configured"
	ConnectionHealthy     = "healthy"
	ConnectionUnreachable = "unreachable"
	ConnectionAuthFailed  = "auth_failed"
)

// Operations domain_operations_total counts. Each is also the name of the span
// the operation opens.
const (
	OpKnowledgeApply   = "knowledge.apply"
	OpMemoryCapture    = "memory.capture"
	OpMemoryManage     = "memory.manage"
	OpSearchQuery      = "search.query"
	OpSearchFetch      = "search.fetch"
	OpCallRecord       = "calls.record"
	OpCallReuse        = "calls.reuse"
	OpCallPromote      = "calls.promote"
	OpCallSweep        = "calls.sweep"
	OpConfigStoreRead  = "configstore.read"
	OpConfigStoreWrite = "configstore.write"
	OpResourceUpload   = "resource.upload"
	OpResourceExtract  = "resource.extract"
	OpTableRegister    = "table.register"
	OpPromptServe      = "prompt.serve"
)

// apply_knowledge sinks and outcomes knowledge_changes_total records.
// SinkInsight is the captured insight itself: created by memory_capture,
// approved or rejected by a review.
const (
	SinkDataHub           = "datahub"
	SinkKnowledgePage     = "knowledge_page"
	SinkAgentInstructions = "agent_instructions"
	SinkInsight           = "insight"

	KnowledgeCreated  = "created"
	KnowledgeApplied  = "applied"
	KnowledgeApproved = "approved"
	KnowledgeRejected = "rejected"
	KnowledgeFailed   = "failed"
)
