package apisvc

import (
	"encoding/json"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/txn2/mcp-data-platform/bench/internal/apigen"
)

// Distractor handlers: every distractor resource serves its seeded rows
// through the standard seven operations, so an agent that calls a wrong
// endpoint gets a coherent answer (the honest failure mode) rather than a
// 404 hint.

// distractorStatusVocab is the distractor lifecycle vocabulary.
var distractorStatusVocab = []string{"active", "archived", "draft"}

// handleDistractor dispatches one distractor operation.
func (s *Service) handleDistractor(w http.ResponseWriter, r *http.Request, op apigen.Operation, rawID string) {
	switch op.Kind {
	case apigen.KindList:
		s.distractorList(w, r, op.Resource)
	case apigen.KindSearch:
		s.distractorSearch(w, r, op.Resource)
	case apigen.KindAggregate:
		s.distractorAggregate(w, r, op.Resource)
	case apigen.KindGet:
		s.distractorGet(w, op.Resource, rawID)
	case apigen.KindCreate:
		s.distractorCreate(w, r, op)
	case apigen.KindUpdate:
		s.distractorUpdate(w, r, op, rawID)
	case apigen.KindDelete:
		s.distractorDelete(w, op.Resource, rawID)
	default:
		writeError(w, http.StatusNotFound, "unknown operation kind "+op.Kind)
	}
}

// distractorList serves a resource's list operation (status and
// created_after filters plus pagination).
func (s *Service) distractorList(w http.ResponseWriter, r *http.Request, key string) {
	status, err := enumParam(r, "status", distractorStatusVocab)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	after, err := parseTimeParam(r, "created_after")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	var items []any
	for _, row := range s.st.distractors[key] {
		if status != "" && row["status"] != status {
			continue
		}
		if !after.IsZero() {
			created, _ := time.Parse(time.RFC3339, rowString(row, "created_at"))
			if created.Before(after) {
				continue
			}
		}
		items = append(items, row)
	}
	s.st.mu.Unlock()
	writePage(w, r, items)
}

// distractorSearch serves a resource's search operation: case-insensitive
// substring match on the name field.
func (s *Service) distractorSearch(w http.ResponseWriter, r *http.Request, key string) {
	var body struct {
		Query string `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Query == "" {
		writeError(w, http.StatusBadRequest, "query is required")
		return
	}
	needle := strings.ToLower(body.Query)
	s.st.mu.Lock()
	var items []any
	for _, row := range s.st.distractors[key] {
		if strings.Contains(strings.ToLower(rowString(row, "name")), needle) {
			items = append(items, row)
		}
	}
	s.st.mu.Unlock()
	writePage(w, r, items)
}

// distractorAggregate serves a resource's aggregate operation (status
// grouping only, per its spec).
func (s *Service) distractorAggregate(w http.ResponseWriter, r *http.Request, key string) {
	if r.URL.Query().Get("group_by") != "status" {
		writeError(w, http.StatusBadRequest, "group_by must be one of: status")
		return
	}
	counts := map[string]int64{}
	s.st.mu.Lock()
	for _, row := range s.st.distractors[key] {
		counts[rowString(row, "status")]++
	}
	s.st.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"groups": sortedGroups(counts, "count")})
}

// distractorGet serves a resource's get-by-id operation.
func (s *Service) distractorGet(w http.ResponseWriter, key, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	if row := findRow(s.st.distractors[key], int64(id)); row != nil {
		writeJSON(w, http.StatusOK, row)
		return
	}
	writeError(w, http.StatusNotFound, "no record with id "+rawID)
}

// distractorCreate serves a resource's create operation: writable fields
// from the body, server-assigned id and creation timestamp.
func (s *Service) distractorCreate(w http.ResponseWriter, r *http.Request, op apigen.Operation) {
	body, err := decodeWritable(r, op)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	rows := s.st.distractors[op.Resource]
	var maxID int64
	for _, row := range rows {
		if id, ok := row["id"].(int64); ok {
			maxID = max(maxID, id)
		}
	}
	row := apigen.Row{"id": maxID + 1, "created_at": createdAtSentinel, "status": "active"}
	maps.Copy(row, body)
	s.st.distractors[op.Resource] = append(rows, row)
	writeJSON(w, http.StatusCreated, row)
}

// distractorUpdate serves a resource's update operation.
func (s *Service) distractorUpdate(w http.ResponseWriter, r *http.Request, op apigen.Operation, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	body, err := decodeWritable(r, op)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	row := findRow(s.st.distractors[op.Resource], int64(id))
	if row == nil {
		writeError(w, http.StatusNotFound, "no record with id "+rawID)
		return
	}
	maps.Copy(row, body)
	writeJSON(w, http.StatusOK, row)
}

// distractorDelete serves a resource's delete operation.
func (s *Service) distractorDelete(w http.ResponseWriter, key, rawID string) {
	id, err := parseID(rawID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.st.mu.Lock()
	defer s.st.mu.Unlock()
	rows := s.st.distractors[key]
	for i, row := range rows {
		if rid, ok := row["id"].(int64); ok && rid == int64(id) {
			s.st.distractors[key] = slices.Delete(rows, i, i+1)
			w.WriteHeader(http.StatusNoContent)
			return
		}
	}
	writeError(w, http.StatusNotFound, "no record with id "+rawID)
}

// decodeWritable decodes a create/update body, keeping only fields the
// operation's request schema declares and validating the status enum.
func decodeWritable(r *http.Request, op apigen.Operation) (map[string]any, error) {
	var raw map[string]any
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, badParam{"invalid JSON body"}
	}
	out := map[string]any{}
	for _, f := range op.Request {
		v, ok := raw[f.Name]
		if !ok {
			continue
		}
		if f.Name == "status" {
			str, _ := v.(string)
			if !slices.Contains(distractorStatusVocab, str) {
				return nil, badParam{"status must be one of: " + strings.Join(distractorStatusVocab, ", ")}
			}
		}
		out[f.Name] = normalizeJSONValue(f, v)
	}
	return out, nil
}

// normalizeJSONValue coerces decoded JSON numbers to the row
// representation (int64 for integer fields).
func normalizeJSONValue(f apigen.Field, v any) any {
	if f.Type != "integer" {
		return v
	}
	if n, ok := v.(float64); ok {
		return int64(n)
	}
	return v
}

// findRow locates a row by id. Callers hold the lock.
func findRow(rows []apigen.Row, id int64) apigen.Row {
	for _, row := range rows {
		if rid, ok := row["id"].(int64); ok && rid == id {
			return row
		}
	}
	return nil
}

// rowString reads a string field from a row.
func rowString(row apigen.Row, key string) string {
	s, _ := row[key].(string)
	return s
}
