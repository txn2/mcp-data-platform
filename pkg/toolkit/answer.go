package toolkit

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// AnswerContract is what a tool always answers one kind of call with: the
// success body a managed script's test is held to when it declares the answer
// a call gets (#1953). The output schema a tool advertises is opened for
// clients (middleware.OpenToolOutputSchema: nothing required, any key
// admitted), so it cannot say what an answer lacks; this is the strict body
// behind it, with its required fields and its closed nested objects.
type AnswerContract struct {
	Tool string
	// Arg and Values select the calls the contract covers by one argument: a
	// call whose Arg is one of Values, "" standing for the argument being
	// absent. An empty Arg covers every call to Tool.
	Arg    string
	Values []string
	Schema *jsonschema.Schema
}

// AnswerContractor is a toolkit that declares what its tools answer.
type AnswerContractor interface {
	AnswerContracts() []AnswerContract
}

// ContractFor is the contract whose body is T, for the calls to tool whose
// arg is one of values. It panics when T cannot be reflected into a schema,
// which is a programming error surfaced by the toolkit's tests.
func ContractFor[T any](tool, arg string, values ...string) AnswerContract {
	schema, err := jsonschema.For[T](nil)
	if err != nil {
		panic(fmt.Sprintf("toolkit.ContractFor %s: %v", tool, err))
	}
	return AnswerContract{Tool: tool, Arg: arg, Values: values, Schema: schema}
}
