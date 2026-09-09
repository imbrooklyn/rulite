package otel

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/imbrooklyn/rulite"
)

// ErrInvalidConfig identifies an invalid adapter option or missing provider.
var ErrInvalidConfig = errors.New("rulite/otel: invalid configuration")

// ErrInvalidAdapter identifies a nil or uninitialized Adapter.
var ErrInvalidAdapter = errors.New("rulite/otel: invalid adapter")

// ErrInvalidExecutor identifies a nil execution interface passed to Fire.
var ErrInvalidExecutor = errors.New("rulite/otel: invalid executor")

// ErrEventLimit is an observation diagnostic indicating that eligible rule
// events exceeded the adapter's recording limit. It is reported at execution
// finish, after summary metrics and the span have been completed. It never
// enters the business ExecutionError or truncates the core Result or Trace.
var ErrEventLimit = errors.New("rulite/otel: trace event limit exceeded")

// Option is an immutable adapter configuration value. Its zero value is a
// no-op. New validates options in order; later options cannot repair an invalid
// earlier option. List options copy their arguments and replace earlier lists.
type Option struct {
	kind   optionKind
	limit  int
	values []string
}

type optionKind uint8

const (
	optionNone optionKind = iota
	optionEventLimit
	optionMetricVersions
	optionRuleMetrics
	optionTraceVersions
	optionHideRuleIDs
	optionTraceRules
)

// WithEventLimit sets the maximum rule events added to each recording span.
// The default is 128; valid limits are 0 through 1024. Filtering precedes this
// limit. The SDK may impose additional limits independently.
func WithEventLimit(limit int) Option { return Option{kind: optionEventLimit, limit: limit} }

// WithMetricVersions enables an exact business-version allowlist for summary
// metrics. At most 16 entries are accepted, each 1 through 128 UTF-8 bytes.
// The value "other" is reserved for unspecified or unlisted versions. An empty
// list records only "other". Snapshot revisions never become metric attributes.
func WithMetricVersions(versions ...rulite.RuleSetVersion) Option {
	return Option{kind: optionMetricVersions, values: copyStrings(versions)}
}

// WithRuleMetrics enables per-rule event counts for an exact RuleID allowlist.
// At most 64 entries of 1 through 128 UTF-8 bytes are accepted. Unknown IDs do
// not produce series. An empty list disables per-rule metrics. No version or
// snapshot revision is attached to these series.
func WithRuleMetrics(ids ...rulite.RuleID) Option {
	return Option{kind: optionRuleMetrics, values: copyStrings(ids)}
}

// WithTraceVersions includes business versions on execution spans. Empty,
// invalid UTF-8, or values longer than 128 bytes are omitted and flagged.
// This option does not enable version attributes on metrics.
func WithTraceVersions() Option { return Option{kind: optionTraceVersions} }

// WithoutTraceRuleIDs omits rule IDs from span events. Priority, execution order,
// phase, and outcome remain available. This does not alter per-rule metrics.
func WithoutTraceRuleIDs() Option { return Option{kind: optionHideRuleIDs} }

// WithTraceRules restricts trace events to an exact RuleID allowlist with at
// most 64 entries of 1 through 128 UTF-8 bytes. An empty list filters all rule
// events. Filtered events are counted separately and do not consume the limit
// or produce a diagnostic. Summary metrics and the core Trace are unaffected.
func WithTraceRules(ids ...rulite.RuleID) Option {
	return Option{kind: optionTraceRules, values: copyStrings(ids)}
}

func copyStrings[S ~string](values []S) []string {
	copy := make([]string, len(values))
	for i, value := range values {
		copy[i] = strings.Clone(string(value))
	}
	return copy
}

type config struct {
	eventLimit     int
	metricVersions []string
	versionMetrics bool
	ruleMetrics    []string
	traceVersions  bool
	hideRuleIDs    bool
	traceRules     map[string]struct{}
}

func configure(options []Option) (config, error) {
	c := config{eventLimit: 128}
	for _, option := range options {
		switch option.kind {
		case optionNone:
		case optionEventLimit:
			if option.limit < 0 || option.limit > 1024 {
				return config{}, ErrInvalidConfig
			}
			c.eventLimit = option.limit
		case optionMetricVersions, optionRuleMetrics, optionTraceRules:
			max := 64
			if option.kind == optionMetricVersions {
				max = 16
			}
			if len(option.values) > max {
				return config{}, ErrInvalidConfig
			}
			for _, value := range option.values {
				if !validText(value) || option.kind == optionMetricVersions && value == "other" {
					return config{}, ErrInvalidConfig
				}
			}
			switch option.kind {
			case optionMetricVersions:
				c.metricVersions, c.versionMetrics = option.values, true
			case optionRuleMetrics:
				c.ruleMetrics = option.values
			case optionTraceRules:
				c.traceRules = make(map[string]struct{}, len(option.values))
				for _, id := range option.values {
					c.traceRules[id] = struct{}{}
				}
			}
		case optionTraceVersions:
			c.traceVersions = true
		case optionHideRuleIDs:
			c.hideRuleIDs = true
		default:
			return config{}, ErrInvalidConfig
		}
	}
	return c, nil
}

func validText(value string) bool {
	return len(value) > 0 && len(value) <= 128 && utf8.ValidString(value)
}
