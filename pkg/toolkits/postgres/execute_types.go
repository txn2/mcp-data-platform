package postgres

import "encoding/json"

// StatementInput is one item of postgres_execute's statements list.
type StatementInput struct {
	SQL    string          `json:"sql"`
	Params json.RawMessage `json:"params,omitempty"`
}

// ExecuteInput is postgres_execute's arguments.
type ExecuteInput struct {
	Connection     string           `json:"connection"`
	SQL            string           `json:"sql,omitempty"`
	Params         json.RawMessage  `json:"params,omitempty"`
	Statements     []StatementInput `json:"statements,omitempty"`
	TimeoutSeconds int              `json:"timeout_seconds,omitempty"`
}

// StatementResult is what one executed statement did.
type StatementResult struct {
	// Command is the statement's command tag word: INSERT, UPDATE, CREATE.
	Command      string   `json:"command"`
	RowsAffected int64    `json:"rows_affected"`
	Columns      []Column `json:"columns"`
	// Rows are the RETURNING rows, at most the connection's max_rows.
	Rows      [][]any `json:"rows"`
	Truncated bool    `json:"truncated"`
}

// ExecuteOutput is postgres_execute's result.
type ExecuteOutput struct {
	Connection string            `json:"connection"`
	Committed  bool              `json:"committed"`
	Statements []StatementResult `json:"statements"`
}
