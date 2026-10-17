package whalert

import (
	"fmt"
	"net/url"
	"sort"
	"time"

	"github.com/txn2/mcp-data-platform/internal/platform/levelalert"
	"github.com/txn2/mcp-data-platform/internal/webhook/whstore"
)

// The spans the per-source conditions are judged over.
const (
	// UnauthorizedSpan is the span the unauthorized share is judged over.
	UnauthorizedSpan = 15 * time.Minute
	// AckSpan is the span the acknowledgement p99 is judged over.
	AckSpan = 5 * time.Minute
	// minUnauthorizedRequests keeps one refused request of two from being
	// half a source's traffic.
	minUnauthorizedRequests = 20
	// percent scales a fraction to the percent the thresholds are written in.
	percent = 100
)

// refusalOutcomes are the answers that asked a sender to retry: events the
// platform could not take.
var refusalOutcomes = []string{"spool_full", "write_failed", "buffer_full"}

// Readings is what one evaluation judges.
type Readings struct {
	Now time.Time
	// Sources are the sources that exist, by name; Enabled is whether each
	// receives.
	Sources map[string]bool
	Spools  []whstore.SpoolState
	// PerSource is each source's request counts, latency and compaction.
	PerSource map[string]*whstore.SourceReadings
	// Grace is how long after a window ends the compactor waits for its
	// segments, so a window is late only past it.
	Grace time.Duration
	// BaseURL is the portal's address; the alerts link to the webhooks
	// page under it. Empty omits the link.
	BaseURL string
}

// Evaluate returns every condition at warning or critical under settings. It
// reads nothing; Readings carries it all.
func Evaluate(settings Settings, r Readings) []levelalert.Finding {
	settings = settings.normalized()
	out := make([]levelalert.Finding, 0, len(r.Spools))
	for _, sp := range r.Spools {
		out = append(out, spoolFindings(settings, sp, r)...)
	}
	names := make([]string, 0, len(r.Sources))
	for name, enabled := range r.Sources {
		if enabled {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		rd := r.PerSource[name]
		if rd == nil {
			rd = &whstore.SourceReadings{Source: name}
		}
		out = append(out, sourceFindings(settings.RulesFor(name), name, rd, r)...)
	}
	return out
}

// collector gathers the findings raised about one subject, a replica or a
// source, each keyed by its condition and the subject.
type collector struct {
	subject string
	link    string
	out     []levelalert.Finding
}

// add records the condition when lvl is a level; an empty lvl is no finding.
func (c *collector) add(cond string, lvl levelalert.Level, title, body string) {
	if lvl != "" {
		c.out = append(c.out, levelalert.Finding{
			Key: "webhook:" + cond + ":" + c.subject, Level: lvl, Title: title, Body: body, Link: c.link,
		})
	}
}

// spoolFindings judges one replica's spool. A replica holding events that
// stopped reporting is critical and judged on nothing else.
func spoolFindings(s Settings, sp whstore.SpoolState, r Readings) []levelalert.Finding {
	c := &collector{subject: sp.Replica, link: page(r.BaseURL, "")}
	if notReporting(c, s, sp, r.Now) {
		return c.out
	}
	// What a replica that stopped reporting last said is no longer its
	// state: a replica replaced in a rollout must not alert on its last
	// report for as long as its row is kept.
	if r.Now.Sub(sp.UpdatedAt) > spoolStale {
		return c.out
	}
	spoolFill(c, s, sp)
	spoolAge(c, s, sp, r.Now)
	drainFailing(c, s, sp, r.Now)
	spoolVolume(c, s, sp)
	return c.out
}

// spoolStale is how long since its last report a replica's spool state is
// read as current, as the webhooks page reads it (two minutes, many times
// the receiver's stats flush).
const spoolStale = 2 * time.Minute

// notReporting adds the critical finding for a replica holding events whose
// last report is older than the threshold, and reports whether it did.
func notReporting(c *collector, s Settings, sp whstore.SpoolState, now time.Time) bool {
	silentFor := now.Sub(sp.UpdatedAt)
	if sp.Events == 0 || s.NotReportingSeconds <= 0 || silentFor.Seconds() < s.NotReportingSeconds {
		return false
	}
	c.add("not_reporting", levelalert.Critical,
		fmt.Sprintf("Replica %s holds %d webhook events and stopped reporting", sp.Replica, sp.Events),
		fmt.Sprintf("Its last report was %s ago, with %d events (%d bytes) waiting to be drained. The threshold is %.0fs.",
			roundDur(silentFor), sp.Events, sp.Bytes, s.NotReportingSeconds))
	return true
}

// spoolFill judges the spool's bytes against its max_bytes.
func spoolFill(c *collector, s Settings, sp whstore.SpoolState) {
	if sp.MaxBytes <= 0 {
		return
	}
	pct := percent * float64(sp.Bytes) / float64(sp.MaxBytes)
	c.add("spool_fill", s.SpoolFillPercent.Of(pct),
		fmt.Sprintf("Webhook spool on %s is %.0f%% full", sp.Replica, pct),
		fmt.Sprintf("%d of %d bytes hold %d events waiting to be drained. At 100%% deliveries are refused with 503.", sp.Bytes, sp.MaxBytes, sp.Events))
}

// spoolAge judges the oldest waiting event's age against the spool's max_age.
func spoolAge(c *collector, s Settings, sp whstore.SpoolState, now time.Time) {
	if sp.Oldest == nil || sp.MaxAge <= 0 {
		return
	}
	age := now.Sub(*sp.Oldest)
	pct := percent * age.Seconds() / sp.MaxAge.Seconds()
	c.add("spool_age", s.SpoolAgePercent.Of(pct),
		fmt.Sprintf("Oldest webhook event on %s has waited %s", sp.Replica, roundDur(age)),
		fmt.Sprintf("That is %.0f%% of the spool's max_age of %s; at max_age deliveries are refused with 503.", pct, sp.MaxAge))
}

// drainFailing judges the drain's failure streak by its count, and as
// critical once it has lasted DrainFailingMinutes.
func drainFailing(c *collector, s Settings, sp whstore.SpoolState, now time.Time) {
	if sp.ConsecutiveFailures <= 0 {
		return
	}
	lvl := s.DrainFailures.Of(float64(sp.ConsecutiveFailures))
	failing := time.Duration(0)
	if sp.FailingSince != nil {
		failing = now.Sub(*sp.FailingSince)
	}
	if s.DrainFailingMinutes > 0 && failing.Minutes() >= s.DrainFailingMinutes {
		lvl = levelalert.Critical
	}
	c.add("drain_failing", lvl,
		fmt.Sprintf("Webhook spool drain on %s has failed %d times in a row", sp.Replica, sp.ConsecutiveFailures),
		fmt.Sprintf("Failing for %s. Last error: %s", roundDur(failing), sp.LastError))
}

// spoolVolume judges the free space left on the volume the spool is on.
func spoolVolume(c *collector, s Settings, sp whstore.SpoolState) {
	if sp.FsTotal == nil || sp.FsFree == nil || *sp.FsTotal <= 0 {
		return
	}
	pct := percent * float64(*sp.FsFree) / float64(*sp.FsTotal)
	c.add("spool_volume", s.SpoolVolumeFreePercent.Of(pct),
		fmt.Sprintf("Webhook spool volume on %s has %.0f%% free", sp.Replica, pct),
		fmt.Sprintf("%d of %d bytes free on the volume holding %s.", *sp.FsFree, *sp.FsTotal, sp.Dir))
}

// sourceFindings judges one source.
func sourceFindings(rules SourceRules, name string, rd *whstore.SourceReadings, r Readings) []levelalert.Finding {
	c := &collector{subject: name, link: page(r.BaseURL, name)}
	refusalFinding(c, rules, rd, r.Now)
	unauthorizedFinding(c, rules, rd, r.Now)
	ackFinding(c, rules, rd)
	c.out = append(c.out, compactionFindings(rules, name, rd, r, c.link)...)
	silentFinding(c, rules, rd, r.Now)
	return c.out
}

// refusalFinding judges the source's refusals over RefusalMinutes.
func refusalFinding(c *collector, rules SourceRules, rd *whstore.SourceReadings, now time.Time) {
	if rules.RefusalMinutes == nil || *rules.RefusalMinutes <= 0 {
		return
	}
	lvl, total := refusals(rd, now, *rules.RefusalMinutes)
	c.add("refusals", lvl,
		fmt.Sprintf("Webhook source %s refused %d deliveries", c.subject, total),
		fmt.Sprintf("Answered with 503 (spool_full, write_failed or buffer_full) in the last %d minutes; the sender retries them.", *rules.RefusalMinutes))
}

// unauthorizedFinding judges the source's unauthorized share over
// UnauthorizedSpan, once it has seen minUnauthorizedRequests.
func unauthorizedFinding(c *collector, rules SourceRules, rd *whstore.SourceReadings, now time.Time) {
	if rules.UnauthorizedPercent == nil {
		return
	}
	pct, total := unauthorizedShare(rd, now.Add(-UnauthorizedSpan))
	if total < minUnauthorizedRequests {
		return
	}
	c.add("unauthorized", rules.UnauthorizedPercent.Of(pct),
		fmt.Sprintf("%.0f%% of webhook source %s's requests were unauthorized", pct, c.subject),
		fmt.Sprintf("Of %d requests in the last 15 minutes. A rotated secret the sender has not picked up looks like this.", total))
}

// ackFinding judges the source's acknowledgement p99 over AckSpan.
func ackFinding(c *collector, rules SourceRules, rd *whstore.SourceReadings) {
	if rules.AckP99MS == nil || rd.Ack.Count <= 0 {
		return
	}
	c.add("ack_p99", rules.AckP99MS.Of(float64(rd.Ack.P99MS)),
		fmt.Sprintf("Webhook source %s acknowledges in %dms at p99", c.subject, rd.Ack.P99MS),
		fmt.Sprintf("Over %d deliveries in the last 5 minutes; the slowest took %dms.", rd.Ack.Count, rd.Ack.MaxMS))
}

// silentFinding warns when a source that opted in has written no segment for
// SilentAfterMinutes.
func silentFinding(c *collector, rules SourceRules, rd *whstore.SourceReadings, now time.Time) {
	if rules.SilentAfterMinutes == nil || *rules.SilentAfterMinutes <= 0 || rd.LastSegmentAt == nil {
		return
	}
	quiet := now.Sub(*rd.LastSegmentAt)
	if quiet.Minutes() < float64(*rules.SilentAfterMinutes) {
		return
	}
	c.add("silent", levelalert.Warning,
		fmt.Sprintf("Webhook source %s has been silent for %s", c.subject, roundDur(quiet)),
		fmt.Sprintf("No events were written since %s. The threshold is %d minutes.", rd.LastSegmentAt.UTC().Format(time.RFC3339), *rules.SilentAfterMinutes))
}

// compactionFindings judges a source's compaction: windows failing, and the
// oldest owed window waiting past its end and the grace.
func compactionFindings(rules SourceRules, name string, rd *whstore.SourceReadings, r Readings, link string) []levelalert.Finding {
	var lvl levelalert.Level
	var title, body string
	if rd.FailingWindows > 0 {
		lvl = levelalert.Warning
		title = fmt.Sprintf("Webhook source %s: %d windows failing to compact", name, rd.FailingWindows)
		body = "Last error: " + rd.LastError
	}
	if over, late := compactionOverdue(rules, rd, r); over != "" && levelalert.Worse(lvl, over) == over {
		lvl = over
		title = fmt.Sprintf("Webhook source %s compaction is %s behind", name, roundDur(late))
		body = fmt.Sprintf("The oldest window owed a compaction ended at %s. %d windows are failing.",
			rd.OldestOwedEnd.UTC().Format(time.RFC3339), rd.FailingWindows)
		if rd.LastError != "" {
			body += " Last error: " + rd.LastError
		}
	}
	if lvl == "" {
		return nil
	}
	return []levelalert.Finding{{Key: "webhook:compaction:" + name, Level: lvl, Title: title, Body: body, Link: link}}
}

// compactionOverdue is the level the oldest owed window's wait past its end
// and the grace reaches, and that wait; no level when nothing is late.
func compactionOverdue(rules SourceRules, rd *whstore.SourceReadings, r Readings) (lvl levelalert.Level, late time.Duration) {
	if rules.CompactionOverdueMinutes == nil || rd.OldestOwedEnd == nil {
		return "", 0
	}
	late = r.Now.Sub(rd.OldestOwedEnd.Add(r.Grace))
	if late <= 0 {
		return "", 0
	}
	return rules.CompactionOverdueMinutes.Of(late.Minutes()), late
}

// refusals is the level and count of a source's refusals over the last
// minutes: any is a warning, one in every minute is critical.
func refusals(rd *whstore.SourceReadings, now time.Time, minutes int) (lvl levelalert.Level, total int64) {
	since := now.Truncate(time.Minute).Add(-time.Duration(minutes-1) * time.Minute)
	withRefusal := 0
	for minute, counts := range rd.Outcomes {
		if minute.Before(since) {
			continue
		}
		var n int64
		for _, o := range refusalOutcomes {
			n += counts[o]
		}
		if n > 0 {
			withRefusal++
			total += n
		}
	}
	switch {
	case withRefusal >= minutes:
		return levelalert.Critical, total
	case total > 0:
		return levelalert.Warning, total
	default:
		return "", 0
	}
}

// unauthorizedShare is the percent of a source's requests since since that
// were refused as unauthorized, and how many requests there were.
func unauthorizedShare(rd *whstore.SourceReadings, since time.Time) (pct float64, total int64) {
	var unauthorized int64
	for minute, counts := range rd.Outcomes {
		if minute.Before(since.Truncate(time.Minute)) {
			continue
		}
		for outcome, n := range counts {
			total += n
			if outcome == "unauthorized" {
				unauthorized += n
			}
		}
	}
	if total == 0 {
		return 0, 0
	}
	return percent * float64(unauthorized) / float64(total), total
}

// page is the webhooks admin page, or a source's, under base.
func page(base, source string) string {
	if base == "" {
		return ""
	}
	if source == "" {
		return base + "/portal/admin/webhooks"
	}
	return base + "/portal/admin/webhooks/" + url.PathEscape(source)
}

// roundDur is a duration as a reader says it.
func roundDur(d time.Duration) time.Duration {
	if d >= time.Minute {
		return d.Round(time.Minute)
	}
	return d.Round(time.Second)
}
