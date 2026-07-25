package apisvc

import (
	"net/http"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// The insights surface (#1054): the perishable listening area whose state
// the world control plane mutates, and the owned-profile area that
// corroborates the credential and carries the durable-contract and
// eternal-invariant behaviors.
//
// The distinction this file exists to serve exactly is: an account with
// nothing provisioned answers HTTP 200 with an empty collection, and an
// unentitled credential answers HTTP 403. Neither is ever served in place
// of the other, and the 403 is identical whether or not monitors exist
// behind it, so a refused credential reveals nothing about provisioning.

// seriesDateLayout is the date form every series point and window
// parameter uses.
const seriesDateLayout = "2006-01-02"

// forbiddenMessage is the documented 403 body's reason. It names the
// entitlement, never the account's provisioning state.
const forbiddenMessage = "the credential is not entitled to the listening product area"

// handleInsights dispatches one insights-family operation.
func (s *Service) handleInsights(w http.ResponseWriter, r *http.Request, opID, id string) {
	switch opID {
	case "list_workspaces":
		writePage(w, r, anySlice(s.fixture.Workspaces))
	case "list_monitors":
		s.listMonitors(w, r)
	case "get_monitor":
		s.getMonitor(w, id)
	case "list_monitor_trend":
		s.listMonitorTrend(w, r, id)
	case "list_profiles":
		writePage(w, r, anySlice(s.fixture.Profiles))
	case "list_profile_metrics":
		s.listProfileMetrics(w, r, id)
	case "aggregate_profile_metrics":
		s.aggregateProfileMetrics(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "unknown operation "+opID)
	}
}

// anySlice widens a typed row slice for the shared paging writer.
func anySlice[T any](rows []T) []any {
	out := make([]any, 0, len(rows))
	for _, r := range rows {
		out = append(out, r)
	}
	return out
}

// listeningDenied writes the documented 403 when the credential is not
// entitled to the listening area, and reports whether it did.
func listeningDenied(w http.ResponseWriter, world apigen.World) bool {
	if world.Listening != apigen.AccessForbidden {
		return false
	}
	writeError(w, http.StatusForbidden, forbiddenMessage)
	return true
}

// provisionedMonitors returns the monitors the world has provisioned: the
// pool's first world.Monitors entries.
func (s *Service) provisionedMonitors(world apigen.World) []apigen.Monitor {
	out := make([]apigen.Monitor, 0, len(s.fixture.Monitors))
	for i, m := range s.fixture.Monitors {
		if i >= world.Monitors {
			break
		}
		out = append(out, m)
	}
	return out
}

// listMonitors serves list_monitors: the account's provisioned listening
// monitors, or an empty collection when it has none.
func (s *Service) listMonitors(w http.ResponseWriter, r *http.Request) {
	world := s.st.currentWorld()
	if listeningDenied(w, world) {
		return
	}
	workspaceID, scoped, err := parseIntParam(r, "workspace_id")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if world.WorkspaceScoped && !scoped {
		writeError(w, http.StatusBadRequest, "workspace_id is required on this account")
		return
	}
	items := make([]any, 0, len(s.fixture.Monitors))
	for _, m := range s.provisionedMonitors(world) {
		if scoped && int64(m.WorkspaceID) != workspaceID {
			continue
		}
		items = append(items, m)
	}
	writePage(w, r, items)
}

// monitor resolves a path id against the provisioned monitors, writing the
// entitlement or not-found response itself. A monitor in the pool but not
// yet provisioned is genuinely absent: it 404s exactly like an id that
// never existed.
func (s *Service) monitor(w http.ResponseWriter, rawID string) (apigen.Monitor, bool) {
	world := s.st.currentWorld()
	if listeningDenied(w, world) {
		return apigen.Monitor{}, false
	}
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return apigen.Monitor{}, false
	}
	for _, m := range s.provisionedMonitors(world) {
		if m.ID == id {
			return m, true
		}
	}
	writeError(w, http.StatusNotFound, "no monitor with id "+rawID)
	return apigen.Monitor{}, false
}

// getMonitor serves get_monitor.
func (s *Service) getMonitor(w http.ResponseWriter, rawID string) {
	m, ok := s.monitor(w, rawID)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, m)
}

// listMonitorTrend serves list_monitor_trend: the daily volume and
// sentiment series that exists only behind a provisioned monitor.
func (s *Service) listMonitorTrend(w http.ResponseWriter, r *http.Request, rawID string) {
	m, ok := s.monitor(w, rawID)
	if !ok {
		return
	}
	win, err := parseSeriesWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	series := s.fixture.Trend[m.ID]
	points := make([]any, 0, len(series))
	for _, p := range series {
		if win.contains(p.Date) {
			points = append(points, p)
		}
	}
	writePage(w, r, points)
}

// profile resolves a path id against the owned profiles. The owned-profile
// area is entitled separately from listening and is populated in every
// world, which is what lets an agent corroborate that an empty monitor
// listing is not a broken credential.
func (s *Service) profile(w http.ResponseWriter, rawID string) (apigen.Profile, bool) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return apigen.Profile{}, false
	}
	p, ok := s.fixture.Profile(id)
	if !ok {
		writeError(w, http.StatusNotFound, "no profile with id "+rawID)
		return apigen.Profile{}, false
	}
	return p, true
}

// listProfileMetrics serves list_profile_metrics.
//
// The granularity parameter is the durable-contract behavior: under
// contract 2026.1 the account accepts it and silently returns daily
// buckets anyway; the 2026.2 release honors it. Nothing in the response
// says which happened, so the only way to know is to look at the dates.
func (s *Service) listProfileMetrics(w http.ResponseWriter, r *http.Request, rawID string) {
	p, ok := s.profile(w, rawID)
	if !ok {
		return
	}
	win, err := parseSeriesWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	granularity, err := parseGranularity(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	series := s.fixture.Metrics[p.ID]
	if granularity == "week" && s.st.currentWorld().Contract == apigen.Contract20262 {
		series = apigen.BucketWeekly(series)
	}
	points := make([]any, 0, len(series))
	for _, point := range series {
		if win.contains(point.Date) {
			points = append(points, point)
		}
	}
	writePage(w, r, points)
}

// aggregateProfileMetrics serves aggregate_profile_metrics: the window's
// totals, with unique reach deduplicated across days rather than summed.
func (s *Service) aggregateProfileMetrics(w http.ResponseWriter, r *http.Request, rawID string) {
	p, ok := s.profile(w, rawID)
	if !ok {
		return
	}
	win, err := parseSeriesWindow(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	series := s.fixture.Metrics[p.ID]
	totals := map[string]int64{"impressions": 0, "engagements": 0}
	uniques := make([]int64, 0, len(series))
	for _, point := range series {
		if !win.contains(point.Date) {
			continue
		}
		totals["impressions"] += point.Impressions
		totals["engagements"] += point.Engagements
		uniques = append(uniques, point.UniqueReach)
	}
	totals["unique_reach"] = apigen.DedupUnique(uniques)
	writeJSON(w, http.StatusOK, map[string]any{"groups": sortedGroups(totals, "value")})
}

// parseGranularity parses and validates the optional granularity
// parameter. An empty value means the default, daily.
func parseGranularity(r *http.Request) (string, error) {
	switch v := r.URL.Query().Get("granularity"); v {
	case "", "day", "week":
		return v, nil
	default:
		return "", badParam{"granularity must be one of: day, week"}
	}
}

// seriesWindow is a parsed reporting window. Both ends are required and
// both are inclusive, as the parameter descriptions document.
type seriesWindow struct{ start, end time.Time }

// parseSeriesWindow parses the required window parameters.
func parseSeriesWindow(r *http.Request) (seriesWindow, error) {
	var win seriesWindow
	var err error
	if win.start, err = parseTimeParam(r, "start_date"); err != nil {
		return win, err
	}
	if win.end, err = parseTimeParam(r, "end_date"); err != nil {
		return win, err
	}
	if win.start.IsZero() || win.end.IsZero() {
		return win, badParam{"start_date and end_date are required"}
	}
	if win.end.Before(win.start) {
		return win, badParam{"end_date must not precede start_date"}
	}
	return win, nil
}

// contains reports whether a series date falls inside the window.
func (win seriesWindow) contains(date string) bool {
	ts, err := time.Parse(seriesDateLayout, date)
	if err != nil {
		return false
	}
	return !ts.Before(win.start) && !ts.After(win.end)
}
