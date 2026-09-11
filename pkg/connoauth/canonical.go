package connoauth

import (
	"fmt"
	"maps"
	"reflect"
	"strings"
)

// Vocabulary values returned by DescribeVocabulary.
const (
	// VocabularyCanonical means every OAuth key on the config is a
	// canonical `oauth_*` key.
	VocabularyCanonical = "canonical"
	// VocabularyLegacy means every OAuth key on the config is a legacy
	// `oauth2_*` key. ParseConfig reads it through the fallback, so the
	// connection works; a save through the admin API rewrites it.
	VocabularyLegacy = "legacy"
	// VocabularyMixed means both vocabularies are present on the same
	// config. ParseConfig resolves it per field with the canonical key
	// winning, so a legacy key whose canonical sibling is set carries a
	// value nothing reads. DescribeVocabulary names those keys.
	VocabularyMixed = "mixed"
)

// legacyPair names a legacy `oauth2_*` config key and the canonical
// `oauth_*` key it becomes. Scope is absent: it changes shape as well as
// name (array to space-delimited string) and is handled on its own.
type legacyPair struct {
	legacy    string
	canonical string
}

// legacyPairs is the whole legacy-to-canonical key mapping, and the one
// place the correspondence is written on the Go side. The browser keeps
// its own copy in ui/src/pages/settings/connections/oauthVocabulary.ts,
// held to this one by TestGoAndBrowserAgreeOnTheOAuthVocabulary.
var legacyPairs = []legacyPair{
	{legacyKeyTokenURL, ConfigKeyTokenURL},
	{legacyKeyAuthorizationURL, ConfigKeyAuthorizationURL},
	{legacyKeyClientID, ConfigKeyClientID},
	{legacyKeyClientSecret, ConfigKeyClientSecret},
	{legacyKeyScopes, ConfigKeyScope},
	{legacyKeyPrompt, ConfigKeyPrompt},
	{legacyKeyEndpointAuthStyle, ConfigKeyEndpointAuthStyle},
}

// Canonicalize returns cfg rewritten onto the canonical `oauth_*`
// vocabulary: each legacy `oauth2_*` key is moved to its canonical
// sibling and dropped, the legacy scope array becomes the canonical
// space-delimited string, and a legacy auth_mode that encoded the grant
// becomes auth_mode=oauth plus the matching oauth_grant. A config that
// is already canonical, or carries no OAuth keys at all, is returned
// unchanged.
//
// This is what keeps the two vocabularies from coexisting on one
// connection. ParseConfig reads both, resolving per field with the
// canonical key winning, which means a legacy key written alongside a
// canonical one holds a value nothing reads -- an operator's credential
// silently shadowed by the one already there (#1682). Running every
// write through here leaves one vocabulary in the row, so the collision
// has nowhere to form.
//
// A legacy key whose canonical sibling already holds a DIFFERENT value
// cannot be resolved by any rule: both values were authored
// deliberately and only the operator knows which is current. That is an
// error naming both keys, wrapping ErrVocabularyConflict, and no value
// is ever included in the message (one of the pairs is a client
// secret). Equal values are not a conflict: the legacy key is dropped.
//
// The input map is not modified. A nil config canonicalizes to an empty
// one, which is what every caller of this already holds: the admin API
// defaults an absent config to an empty map before it gets here.
func Canonicalize(cfg map[string]any) (map[string]any, error) {
	out := make(map[string]any, len(cfg))
	maps.Copy(out, cfg)
	var conflicts []string
	for _, pair := range legacyPairs {
		raw, ok := out[pair.legacy]
		if !ok {
			continue
		}
		value, err := translateLegacyValue(pair, raw)
		if err != nil {
			return nil, err
		}
		existing, held := out[pair.canonical]
		switch {
		case !held:
			out[pair.canonical] = value
		case !reflect.DeepEqual(existing, value):
			// continue targets the loop, not the switch: the legacy key is
			// left where it is, because this config is refused whole.
			conflicts = append(conflicts, fmt.Sprintf("%s and %s hold different values", pair.canonical, pair.legacy))
			continue
		}
		delete(out, pair.legacy)
	}
	if conflict := canonicalizeAuthMode(out); conflict != "" {
		conflicts = append(conflicts, conflict)
	}
	if len(conflicts) > 0 {
		return nil, fmt.Errorf(
			"oauth config carries both the canonical oauth_* and the legacy oauth2_* vocabulary and they disagree (%s); "+
				"keep the canonical key and remove the legacy one: %w",
			strings.Join(conflicts, "; "), ErrVocabularyConflict)
	}
	return out, nil
}

// translateLegacyValue converts a legacy value into the shape its
// canonical key holds. Every pair but scope is a rename: the value is
// carried across untouched, whatever its type, so a malformed value is
// reported by ParseConfig in the vocabulary the operator will be
// reading from then on. Scope changes shape, from the legacy array to
// the canonical space-delimited string (the OAuth 2.0 wire form).
func translateLegacyValue(pair legacyPair, raw any) (any, error) {
	if pair.legacy != legacyKeyScopes {
		return raw, nil
	}
	scopes, err := coerceScopeArray(raw)
	if err != nil {
		return nil, err
	}
	return strings.Join(scopes, " "), nil
}

// canonicalizeAuthMode rewrites a legacy auth_mode in place to the
// canonical AuthModeOAuth plus the grant the mode encoded. It returns a
// non-empty conflict description when an explicit oauth_grant
// contradicts the grant the mode names: the two were authored
// separately and picking either silently would change which flow the
// connection runs.
func canonicalizeAuthMode(cfg map[string]any) string {
	var grant string
	switch getStringValue(cfg, ConfigKeyAuthMode) {
	case legacyAuthModeAuthorizationCode:
		grant = GrantAuthorizationCode
	case legacyAuthModeClientCredentials:
		grant = GrantClientCredentials
	default:
		return ""
	}
	if held := getStringValue(cfg, ConfigKeyGrant); held != "" && held != grant {
		return fmt.Sprintf("%s names the %s grant and %s says %s", ConfigKeyAuthMode, grant, ConfigKeyGrant, held)
	}
	cfg[ConfigKeyAuthMode] = AuthModeOAuth
	cfg[ConfigKeyGrant] = grant
	return ""
}

// DescribeVocabulary reports which OAuth config vocabulary cfg carries
// -- VocabularyCanonical, VocabularyLegacy, VocabularyMixed, or "" when
// it carries no OAuth configuration at all (a nil config included) --
// together with the legacy keys whose value nothing reads because the
// canonical sibling is set.
//
// It is what lets an operator see the collision where they are already
// looking. A mixed connection reads as fully configured on every
// surface: oauth-status reports the shape it resolved and says nothing
// about the value it ignored, and the portal's configuration table
// lists both sets as equals. The shadowed keys are the ones to strike
// through.
//
// auth_mode appears among the shadowed keys when it names a legacy
// grant and an explicit oauth_grant is also present: the grant is read
// from oauth_grant, so the mode's half of the name is inert.
func DescribeVocabulary(cfg map[string]any) (vocabulary string, shadowedKeys []string) {
	canonicalKeys, legacyKeys, shadowed := describeVocabularyKeys(cfg)
	canonicalMode, legacyMode, shadowedMode := describeVocabularyMode(cfg)
	if shadowedMode {
		shadowed = append(shadowed, ConfigKeyAuthMode)
	}
	canonical := canonicalKeys || canonicalMode
	legacy := legacyKeys || legacyMode
	switch {
	case canonical && legacy:
		return VocabularyMixed, shadowed
	case canonical:
		return VocabularyCanonical, nil
	case legacy:
		return VocabularyLegacy, nil
	default:
		return "", nil
	}
}

// describeVocabularyKeys reports which vocabularies the config's OAuth
// keys belong to, and the legacy keys whose canonical sibling is set.
func describeVocabularyKeys(cfg map[string]any) (canonical, legacy bool, shadowed []string) {
	for _, pair := range legacyPairs {
		_, heldLegacy := cfg[pair.legacy]
		_, heldCanonical := cfg[pair.canonical]
		if heldCanonical {
			canonical = true
		}
		if heldLegacy {
			legacy = true
		}
		if heldLegacy && heldCanonical {
			shadowed = append(shadowed, pair.legacy)
		}
	}
	return canonical, legacy, shadowed
}

// describeVocabularyMode reports the same for the auth_mode half, where
// the canonical form carries the grant in a key of its own. The mode is
// shadowed when it names a legacy grant and an explicit oauth_grant is
// present: the grant is read from oauth_grant.
func describeVocabularyMode(cfg map[string]any) (canonical, legacy, shadowedMode bool) {
	mode := getStringValue(cfg, ConfigKeyAuthMode)
	legacyMode := mode == legacyAuthModeAuthorizationCode || mode == legacyAuthModeClientCredentials
	_, heldGrant := cfg[ConfigKeyGrant]
	return mode == AuthModeOAuth || heldGrant, legacyMode, legacyMode && heldGrant
}
