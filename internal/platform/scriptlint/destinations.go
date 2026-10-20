package scriptlint

import (
	"fmt"
	"slices"

	"github.com/txn2/mcp-data-platform/internal/platform/scriptrun"
	"github.com/txn2/mcp-data-platform/internal/scriptdest"
	"github.com/txn2/mcp-data-platform/pkg/script"
)

// CheckDestinations reports each destination a source names literally that the
// deployment does not declare.
//
// scriptrun.Validate is deployment-independent — it parses source and reads what
// the code reaches — so this is a separate pass over its report, applied by the
// surfaces that know the configured set. Splitting it that way keeps a save
// working when configuration changes underneath a stored script, while the
// surface whose job is answering "would this run" answers it (#1415).
//
// It reads report.Destinations, which holds only the destinations named as
// string literals in the source. A call that computes its destination is
// invisible there and is reported by report.DynamicDestinations instead: its
// address is not readable from the source, so there is nothing to check.
//
// The refusal is scriptdest.Resolve's, so validate and the run say the same
// thing about the same script.
func CheckDestinations(report scriptrun.Report, declared []script.Destination) []scriptrun.Finding {
	var findings []scriptrun.Finding
	for _, name := range report.Destinations {
		if _, err := scriptdest.Resolve(name, declared); err != nil {
			findings = append(findings, scriptrun.Finding{
				Severity: scriptrun.SeverityError,
				Message:  err.Error(),
				Hint: "Name a destination this deployment declares, or write to " +
					script.DestinationPortal + " or " + script.DestinationResources +
					", which are always available. " +
					"A destination is deployment configuration (scripts.destinations), " +
					"not something the script can add.",
			})
		}
	}
	return findings
}

// WithDestinationCheck returns report with CheckDestinations' findings folded
// in and OK recomputed, which is the whole of what a validating surface does
// with them. It exists so the tool arm and the portal editor cannot fold them
// in differently.
func WithDestinationCheck(report scriptrun.Report, declared []script.Destination) scriptrun.Report {
	found := CheckDestinations(report, declared)
	if len(found) == 0 {
		return report
	}
	// Built fresh rather than appended onto the caller's slice: the two share a
	// backing array, and sorting in place would reorder findings the caller
	// still holds.
	merged := make([]scriptrun.Finding, 0, len(report.Findings))
	merged = append(merged, report.Findings...)
	merged = append(merged, found...)
	slices.SortStableFunc(merged, byLine)
	report.Findings = merged
	report.OK = !slices.ContainsFunc(merged, func(f scriptrun.Finding) bool { return f.Severity == scriptrun.SeverityError })
	return report
}

// DraftRefusal is why a draft of source is not run, or "" when it is: the
// static read every surface applies before a draft executes, so a draft
// refuses what the rest of the surface refuses -- a source that cannot parse,
// one carrying an inline credential, one naming a destination this deployment
// does not declare -- and the destination refusal arrives before the script's
// queries have run rather than after (#1415). The authoring gates are not
// applied: a draft is how an author tries source before it is finished.
func DraftRefusal(source string, destinations []script.Destination) string {
	report := WithDestinationCheck(scriptrun.Validate(source), destinations)
	if report.OK {
		return ""
	}
	detail := "the source does not pass validation, so it was not run"
	if len(report.Findings) > 0 {
		detail += ": " + report.Findings[0].Message
	}
	return detail
}

// byLine orders findings top to bottom.
func byLine(a, b scriptrun.Finding) int { return a.Line - b.Line }

// RegisterCheck answers whether the caller could register a table on a
// connection: nil when it could, the refusal register would give when not.
type RegisterCheck func(connection string) error

// WithRegisterCheck returns report with a finding for each connection an
// export's register= names literally that check refuses, and OK recomputed
// (#2213). Like the destination check it is a pass over the
// deployment-independent report, applied by the surface that knows the
// deployment and the caller; the run asks the same question before it writes,
// so validate and the run refuse the same register= target. A nil check
// leaves the report as it is.
func WithRegisterCheck(report scriptrun.Report, check RegisterCheck) scriptrun.Report {
	if check == nil || len(report.RegisterConnections) == 0 {
		return report
	}
	merged := make([]scriptrun.Finding, 0, len(report.Findings))
	merged = append(merged, report.Findings...)
	added := false
	for _, conn := range report.RegisterConnections {
		if err := check(conn); err != nil {
			added = true
			merged = append(merged, scriptrun.Finding{
				Severity: scriptrun.SeverityError,
				Message:  fmt.Sprintf("register names connection %q, which cannot hold the table: %v", conn, err),
				Hint:     "Name a connection a table can be registered on; the export checks this before it writes, and a run naming this one fails with nothing written.",
			})
		}
	}
	if !added {
		return report
	}
	slices.SortStableFunc(merged, byLine)
	report.Findings = merged
	report.OK = !slices.ContainsFunc(merged, func(f scriptrun.Finding) bool { return f.Severity == scriptrun.SeverityError })
	return report
}
