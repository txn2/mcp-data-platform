package tuning

const (
	// defaultQualityThreshold is the minimum quality score for data access.
	defaultQualityThreshold = 0.7

	// defaultMaxQueryLimit is the maximum number of rows a query can return.
	defaultMaxQueryLimit = 10000
)

// Rules defines operational rules for the platform.
type Rules struct {
	// RequireDataHubCheck requires checking DataHub before writing queries.
	RequireDataHubCheck bool `yaml:"require_datahub_check"`

	// WarnOnDeprecated warns when accessing deprecated entities.
	WarnOnDeprecated bool `yaml:"warn_on_deprecated"`

	// QualityThreshold is the minimum quality score for data access.
	QualityThreshold float64 `yaml:"quality_threshold"`

	// MaxQueryLimit is the maximum number of rows a query can return.
	MaxQueryLimit int `yaml:"max_query_limit"`

	// RequirePIIAcknowledgment requires acknowledgment when accessing PII data.
	RequirePIIAcknowledgment bool `yaml:"require_pii_acknowledgment"`

	// Custom rules
	Custom map[string]any `yaml:"custom,omitempty"`
}

// DefaultRules returns sensible default rules.
func DefaultRules() *Rules {
	return &Rules{
		RequireDataHubCheck:      true,
		WarnOnDeprecated:         true,
		QualityThreshold:         defaultQualityThreshold,
		MaxQueryLimit:            defaultMaxQueryLimit,
		RequirePIIAcknowledgment: false,
		Custom:                   make(map[string]any),
	}
}

// RuleEngine evaluates rules against actions.
type RuleEngine struct {
	rules *Rules
}

// NewRuleEngine creates a new rule engine.
func NewRuleEngine(rules *Rules) *RuleEngine {
	if rules == nil {
		rules = DefaultRules()
	}
	return &RuleEngine{rules: rules}
}

// Violation represents a rule violation.
type Violation struct {
	Rule       string
	Severity   Severity
	Message    string
	Suggestion string
}

// Severity indicates the severity of a violation.
type Severity string

// Severity levels for operational rules.
const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// CheckQueryExecution checks rules for query execution.
func (e *RuleEngine) CheckQueryExecution(metadata QueryMetadata) []Violation {
	var violations []Violation

	// Check quality threshold
	if metadata.QualityScore != nil && *metadata.QualityScore < e.rules.QualityThreshold {
		violations = append(violations, Violation{
			Rule:       "quality_threshold",
			Severity:   SeverityWarning,
			Message:    "Data quality score is below threshold",
			Suggestion: "Verify data quality before using results",
		})
	}

	// Check deprecation
	if e.rules.WarnOnDeprecated && metadata.IsDeprecated {
		violations = append(violations, Violation{
			Rule:       "deprecated_data",
			Severity:   SeverityWarning,
			Message:    "Accessing deprecated data",
			Suggestion: metadata.DeprecationNote,
		})
	}

	// Check PII
	if e.rules.RequirePIIAcknowledgment && metadata.ContainsPII {
		violations = append(violations, Violation{
			Rule:       "pii_access",
			Severity:   SeverityInfo,
			Message:    "This data contains PII",
			Suggestion: "Handle with appropriate care and access controls",
		})
	}

	return violations
}

// QueryMetadata provides context for rule evaluation.
type QueryMetadata struct {
	QualityScore    *float64
	IsDeprecated    bool
	DeprecationNote string
	ContainsPII     bool
	RowCount        *int64
}

// ShouldRequireDataHubCheck returns whether DataHub check is required.
func (e *RuleEngine) ShouldRequireDataHubCheck() bool {
	return e.rules.RequireDataHubCheck
}

// GetMaxQueryLimit returns the maximum query limit.
func (e *RuleEngine) GetMaxQueryLimit() int {
	return e.rules.MaxQueryLimit
}

// GetCustomRule retrieves a custom rule value.
func (e *RuleEngine) GetCustomRule(name string) (any, bool) {
	v, ok := e.rules.Custom[name]
	return v, ok
}
