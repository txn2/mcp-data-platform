// Package ftsquery is the one place the platform's full-text searches build
// the tsquery they match a caller's words with (#2838).
//
// A search that ranks with an embedding as well (hybrid) matches every word:
// the vector arm already returns the rows that are near the intent whatever
// their words, so the lexical arm adds the rows that hold the whole phrase. A
// search that ranks by words alone (no embedder, or the embedder did not
// answer in time) matches any word and ranks by how many match and how well:
// an agent's intent is a sentence, and a sentence whose every word is in one
// row matches almost nothing, so requiring all of them answered "outbound
// orders count" with no rows at all.
package ftsquery

// AllWords is the tsquery matching rows that hold every word of the text bound
// at param (a placeholder such as "$2" or "?"): plainto_tsquery, whose
// lexemes are joined with &.
func AllWords(param string) string {
	return "plainto_tsquery('english', " + param + ")"
}

// AnyWord is the tsquery matching rows that hold at least one word of the text
// bound at param. It is plainto_tsquery's query with every & made |: the same
// lexemes, stemmed and stop words removed, by the same configuration. The
// default parser never puts a space inside a lexeme, so " & " occurs in the
// query's text only between lexemes (a URL's & is inside its quoted lexeme),
// and the text form of a tsquery is valid tsquery input. Text of stop words
// alone gives an empty query, which matches nothing, as plainto_tsquery's
// does.
func AnyWord(param string) string {
	return "replace(" + AllWords(param) + "::text, ' & ', ' | ')::tsquery"
}

// For is the tsquery a search matches with: AllWords when it also ranks with
// an embedding, AnyWord when it ranks by words alone.
func For(param string, hybrid bool) string {
	if hybrid {
		return AllWords(param)
	}
	return AnyWord(param)
}
