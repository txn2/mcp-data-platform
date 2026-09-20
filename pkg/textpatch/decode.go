package textpatch

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// editKeys is every key an edit object may carry, and is the accepted set a
// refusal names. It is derived from the shared schema rather than typed a
// second time, so a property added to the grammar is accepted by the decoder
// without anyone remembering to add it here.
var editKeys = func() map[string]bool {
	props, ok := PropertiesMap()["edits"].(map[string]any)
	if !ok {
		panic("textpatch: PropertiesJSON has no \"edits\" property")
	}
	items, ok := props["items"].(map[string]any)
	if !ok {
		panic("textpatch: the \"edits\" property declares no items")
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		panic("textpatch: the \"edits\" items declare no properties")
	}
	keys := make(map[string]bool, len(itemProps))
	for name := range itemProps {
		keys[name] = true
	}
	return keys
}()

// UnmarshalJSON decodes one edit and records what the caller actually sent,
// which is more than the decoded value can say.
//
// A string field cannot distinguish "the key was absent" from "the key was an
// empty string", and for the payload keys those two mean opposite things: an
// explicit empty replacement deletes the matched text, while an absent one is a
// caller who meant to supply text and named the key wrong. Decoded into a
// plain struct both arrive as "", so a misspelled payload key used to delete
// the anchor and report success (#1804). The presence of each key is therefore
// carried alongside the value, and Apply refuses the omission.
//
// Unknown keys are collected here rather than rejected here so the refusal
// reaches the caller as a patch error with a code, an edit index and a hint,
// the same shape every other correctable patch failure has.
func (e *Edit) UnmarshalJSON(data []byte) error {
	// A distinct type with no methods, so decoding the value does not call
	// this function again.
	type editValue Edit
	var value editValue
	if err := json.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("textpatch: decoding an edit: %w", err)
	}
	var sent map[string]json.RawMessage
	if err := json.Unmarshal(data, &sent); err != nil {
		return fmt.Errorf("textpatch: reading the keys an edit carries: %w", err)
	}

	*e = Edit(value)
	_, hasReplace := sent["replace"]
	_, hasText := sent["text"]
	e.replaceOmitted = !hasReplace
	e.textOmitted = !hasText
	for key := range sent {
		if !editKeys[key] {
			e.unknownKeys = append(e.unknownKeys, key)
		}
	}
	sort.Strings(e.unknownKeys)
	return nil
}

// checkEdit refuses an edit the caller did not finish writing: one carrying a
// key the grammar has no meaning for, or one whose operation takes its payload
// from a key that was not sent.
//
// It runs before the anchor is resolved, so a refused edit changes nothing —
// and because Apply aborts on the first failure, neither does any edit beside
// it in the same call.
func (e Edit) checkEdit(index int) error {
	if len(e.unknownKeys) > 0 {
		return newError(CodeBadEdit, index,
			"Use only the keys the edit grammar declares: "+acceptedEditKeys()+".",
			"edit carries unknown key(s) %s", quoteList(e.unknownKeys))
	}
	switch e.op() {
	case OpReplace:
		if e.replaceOmitted {
			return newError(CodeBadEdit, index,
				`Put the new text under "replace". To delete the matched text instead, send "replace": "" explicitly.`,
				`op %q carries no "replace" key`, e.op())
		}
	case OpInsertBefore, OpInsertAfter, OpReplaceSection, OpReplaceContent, OpAppend, OpPrepend:
		if e.textOmitted {
			return newError(CodeBadEdit, index,
				`Put the text this operation writes under "text". To empty the target instead, send "text": "" explicitly.`,
				`op %q carries no "text" key`, e.op())
		}
	}
	return nil
}

// acceptedEditKeys renders the accepted key set for a refusal, sorted so the
// message reads the same every time.
func acceptedEditKeys() string {
	keys := make([]string, 0, len(editKeys))
	for name := range editKeys {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return quoteList(keys)
}

// quoteList renders names as a quoted, comma-separated list.
func quoteList(names []string) string {
	quoted := make([]string, len(names))
	for i, name := range names {
		quoted[i] = fmt.Sprintf("%q", name)
	}
	return strings.Join(quoted, ", ")
}
