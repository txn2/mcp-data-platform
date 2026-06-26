package knowledgepage

// This file is the producer side of the reference scheme for callers outside the
// page-authoring path: list/get tools and search providers that hold an entity
// and need the canonical mcp:<type>:<key> string an agent can cite. The builders
// self-validate by round-tripping through ParseEntityRef and return "" when the
// inputs cannot form a resolvable reference (for example a file-defined prompt
// whose id is not a UUID and therefore has no prompts row to reference), so a
// caller can assign the result to an omitempty field and never emit a reference
// the parser would reject.

// AssetRef returns the canonical reference for an asset, or "" if id is empty.
func AssetRef(id string) string {
	return refOrEmpty(EntityRef{TargetType: RefTargetAsset, AssetID: id})
}

// PromptRef returns the canonical reference for a prompt, or "" when id is not a
// UUID (file-defined prompts have no prompts row and are not referenceable).
func PromptRef(id string) string {
	return refOrEmpty(EntityRef{TargetType: RefTargetPrompt, PromptID: id})
}

// PageReference returns the canonical reference for a knowledge page, or "" if id
// is empty. It is not named PageRef to avoid colliding with the PageRef type.
func PageReference(id string) string {
	return refOrEmpty(EntityRef{TargetType: RefTargetKnowledgePage, RefPageID: id})
}

// ConnectionRef returns the canonical reference for a connection, or "" if kind
// or name is empty.
func ConnectionRef(kind, name string) string {
	return refOrEmpty(EntityRef{TargetType: RefTargetConnection, ConnectionKind: kind, ConnectionName: name})
}

// refOrEmpty serializes a reference and returns it only when it round-trips
// through ParseEntityRef, so an unresolvable reference is never emitted.
func refOrEmpty(r EntityRef) string {
	s := r.URN()
	if _, err := ParseEntityRef(s); err != nil {
		return ""
	}
	return s
}
