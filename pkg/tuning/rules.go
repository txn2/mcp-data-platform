package tuning

const (
	// defaultQualityThreshold is the minimum quality score for data access.
	defaultQualityThreshold = 0.7

	// defaultMaxQueryLimit is the maximum number of rows a query can return.
	defaultMaxQueryLimit = 10000
)

// Rules defines operational rules for the platform.
type Rules struct {
	// QualityThreshold is the minimum quality score for data access.
	QualityThreshold float64 `yaml:"quality_threshold"`

	// MaxQueryLimit is the maximum number of rows a query can return.
	MaxQueryLimit int `yaml:"max_query_limit"`

	// Custom rules
	Custom map[string]any `yaml:"custom,omitempty"`
}

// DefaultRules returns sensible default rules.
func DefaultRules() *Rules {
	return &Rules{
		QualityThreshold: defaultQualityThreshold,
		MaxQueryLimit:    defaultMaxQueryLimit,
		Custom:           make(map[string]any),
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

// GetMaxQueryLimit returns the maximum query limit.
func (e *RuleEngine) GetMaxQueryLimit() int {
	return e.rules.MaxQueryLimit
}

// GetCustomRule retrieves a custom rule value.
func (e *RuleEngine) GetCustomRule(name string) (any, bool) {
	v, ok := e.rules.Custom[name]
	return v, ok
}
