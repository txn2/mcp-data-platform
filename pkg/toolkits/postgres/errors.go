package postgres

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

// StatementError is a PostgreSQL error as the caller reads it: everything the
// server said about the failure, under the field names its protocol uses.
//
// It is returned to the caller alone. Message, Detail, Hint and Where can
// repeat a value from the data or from a bound parameter ("Key (email)=(...)
// already exists", "invalid input syntax for type integer: \"...\""), so none
// of them reaches the result's first text block, which is what the audit row's
// error text, the reflexive capture and the logs read: those carry Summary,
// built from the SQLSTATE and the names of the objects involved.
type StatementError struct {
	SQLState   string `json:"sqlstate"`
	Condition  string `json:"condition,omitempty"`
	Message    string `json:"message"`
	Detail     string `json:"detail,omitempty"`
	Hint       string `json:"hint,omitempty"`
	Where      string `json:"where,omitempty"`
	Schema     string `json:"schema,omitempty"`
	Table      string `json:"table,omitempty"`
	Column     string `json:"column,omitempty"`
	Constraint string `json:"constraint,omitempty"`
	DataType   string `json:"data_type,omitempty"`
	Position   int32  `json:"position,omitempty"`
	// Statement is the index of the failed statement in a statements list.
	Statement *int `json:"statement,omitempty"`
}

// summary is the failure in words that name no value: the SQLSTATE, its
// condition, and the objects the server named.
func (e *StatementError) summary() string {
	var b strings.Builder
	_, _ = b.WriteString("postgres: statement failed with SQLSTATE ")
	_, _ = b.WriteString(e.SQLState)
	if e.Condition != "" {
		fmt.Fprintf(&b, " (%s)", e.Condition)
	}
	if e.Statement != nil {
		fmt.Fprintf(&b, " in statement %d", *e.Statement)
	}
	for _, part := range []struct{ name, value string }{
		{"constraint", e.Constraint}, {"table", qualified(e.Schema, e.Table)}, {"column", e.Column}, {"type", e.DataType},
	} {
		if part.value != "" {
			fmt.Fprintf(&b, "; %s %s", part.name, part.value)
		}
	}
	return b.String()
}

func qualified(schema, table string) string {
	if table == "" || schema == "" {
		return table
	}
	return schema + "." + table
}

// statementError reads a PostgreSQL error, or returns nil for any other.
func statementError(err error) *StatementError {
	pg, ok := errors.AsType[*pgconn.PgError](err)
	if !ok {
		return nil
	}
	return &StatementError{
		SQLState:   pg.Code,
		Condition:  conditionNames[pg.Code],
		Message:    pg.Message,
		Detail:     pg.Detail,
		Hint:       pg.Hint,
		Where:      pg.Where,
		Schema:     pg.SchemaName,
		Table:      pg.TableName,
		Column:     pg.ColumnName,
		Constraint: pg.ConstraintName,
		DataType:   pg.DataTypeName,
		Position:   pg.Position,
	}
}

// callerText is the server's own account of the failure, for the caller.
func callerText(se *StatementError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "PostgreSQL said: %s", se.Message)
	for _, part := range []struct{ name, value string }{{"DETAIL", se.Detail}, {"HINT", se.Hint}, {"WHERE", se.Where}} {
		if part.value != "" {
			fmt.Fprintf(&b, "\n%s: %s", part.name, part.value)
		}
	}
	return b.String()
}

// scrubDriverError keeps what a connection failure says about the network and
// the handshake, and drops what pgconn's error repeats of the connection
// settings: the user, the database and, through a password failure, nothing of
// the password itself (pgconn never echoes it, but the settings are not the
// caller's to read either).
func scrubDriverError(err error) string {
	if ce, ok := errors.AsType[*pgconn.ConnectError](err); ok {
		if inner := errors.Unwrap(ce); inner != nil {
			if se := statementError(inner); se != nil {
				return se.summary()
			}
			return firstLine(inner.Error())
		}
	}
	return firstLine(err.Error())
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// conditionNames are the condition names of the SQLSTATEs a caller most often
// meets, as PostgreSQL's errcodes appendix spells them.
var conditionNames = map[string]string{
	"23502": "not_null_violation",
	"23503": "foreign_key_violation",
	"23505": "unique_violation",
	"23514": "check_violation",
	"23P01": "exclusion_violation",
	"22001": "string_data_right_truncation",
	"22003": "numeric_value_out_of_range",
	"22007": "invalid_datetime_format",
	"22008": "datetime_field_overflow",
	"22012": "division_by_zero",
	"22P02": "invalid_text_representation",
	"25006": "read_only_sql_transaction",
	"40001": "serialization_failure",
	"40P01": "deadlock_detected",
	"42501": "insufficient_privilege",
	"42601": "syntax_error",
	"42703": "undefined_column",
	"42883": "undefined_function",
	"42P01": "undefined_table",
	"42P02": "undefined_parameter",
	"42804": "datatype_mismatch",
	"55P03": "lock_not_available",
	"57014": "query_canceled",
	"08P01": "protocol_violation",
	"0A000": "feature_not_supported",
}
