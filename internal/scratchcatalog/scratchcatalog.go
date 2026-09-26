// Package scratchcatalog reads the query engine's refusals of the scratch
// catalog's partition procedures and states the setting that fixes each
// (#1888). The webhook source pre-check and the Trino connection test both
// meet these refusals, and an operator reading either needs the same sentence:
// which rule or property to add, and where the setup is written down.
//
// It knows the three procedures the platform calls and the shape of Trino's
// answers, nothing about connections or sources.
package scratchcatalog

import (
	"errors"
	"regexp"
	"strings"
)

// The partition procedures the platform calls on a scratch catalog. Webhook
// sources use all three; registered tables use none.
const (
	SyncPartitionMetadata = "sync_partition_metadata"
	RegisterPartition     = "register_partition"
	UnregisterPartition   = "unregister_partition"
)

// Procedures is every partition procedure the platform calls, in the order a
// check reports them.
var Procedures = []string{SyncPartitionMetadata, RegisterPartition, UnregisterPartition}

// DocsAccessControl and DocsCatalog are where the setup is written down.
const (
	DocsAccessControl = "docs/server/scratch-catalog.md#access-control"
	DocsCatalog       = "docs/server/scratch-catalog.md#the-catalog"
)

// ErrProcedureDenied is the engine's access control refusing a partition
// procedure to the connection's Trino user.
var ErrProcedureDenied = errors.New("the scratch connection's Trino user may not execute a partition procedure")

// ErrRegisterDisabled is the catalog refusing register_partition because
// hive.allow-register-partition-procedure is not set.
var ErrRegisterDisabled = errors.New("the scratch catalog does not allow register_partition")

// deniedRe matches Trino's refusal of a procedure by access control:
// "Access Denied: Cannot execute procedure scratch.system.unregister_partition".
var deniedRe = regexp.MustCompile(`Access Denied: Cannot execute procedure ([^\s:]+)`)

// Denied reports the procedure an error says the user may not execute, as
// Trino names it (catalog.schema.procedure), or false when the error is not
// that refusal.
func Denied(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	m := deniedRe.FindStringSubmatch(err.Error())
	if m == nil {
		return "", false
	}
	return m[1], true
}

// Disabled reports whether an error is the catalog refusing register_partition
// for want of hive.allow-register-partition-procedure.
func Disabled(err error) bool {
	return err != nil && strings.Contains(err.Error(), "procedure is disabled")
}

// DeniedRemedy is the sentence an operator acts on when the Trino user of
// connection may not execute procedure (as Denied reported it).
func DeniedRemedy(connection, procedure string) string {
	return "the Trino user of connection " + connection + " may not EXECUTE " + procedure +
		"; a catalog rule does not grant procedures, so add a procedures rule for that user on the catalog's system schema (see " +
		DocsAccessControl + ")"
}

// DisabledRemedy is the sentence an operator acts on when catalog refuses
// register_partition.
func DisabledRemedy(catalog string) string {
	return "the " + catalog + " catalog does not allow register_partition; set hive.allow-register-partition-procedure=true in its catalog properties and restart the coordinator and every worker (see " +
		DocsCatalog + ")"
}

// Classify wraps err with the sentence that fixes it when it is one of the two
// refusals, keeping err and the matching sentinel for errors.Is. Any other
// error is returned as it is.
func Classify(connection, catalog string, err error) error {
	if procedure, ok := Denied(err); ok {
		return &refusal{msg: DeniedRemedy(connection, procedure), kind: ErrProcedureDenied, err: err}
	}
	if Disabled(err) {
		return &refusal{msg: DisabledRemedy(catalog), kind: ErrRegisterDisabled, err: err}
	}
	return err
}

// refusal is a classified error: the remedy first, then the engine's words.
type refusal struct {
	msg  string
	kind error
	err  error
}

func (r *refusal) Error() string   { return r.msg + ": " + r.err.Error() }
func (r *refusal) Unwrap() []error { return []error{r.kind, r.err} }
