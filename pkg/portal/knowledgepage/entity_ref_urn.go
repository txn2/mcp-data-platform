package knowledgepage

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// mcpScheme prefixes the serialized form of an internal-entity reference, for
// example "mcp:asset:<id>" or "mcp:connection:(trino,warehouse)". It is the
// platform's existing internal scheme (pkg/resource uses "mcp" as well). The
// external DataHub case keeps its native "urn:li:" URN.
const mcpScheme = "mcp:"

// externalURNPrefix marks an external (DataHub or other catalog) reference. A
// reference string with this prefix is stored verbatim in entity_urn.
const externalURNPrefix = "urn:"

// URN returns the serialized projection of a reference: the mcp: form for an
// internal entity (resolved by id, so a rename does not change it), or the
// external URN as-is for a DataHub reference. The empty string is returned for an
// unrecognized target type.
func (r EntityRef) URN() string {
	switch r.TargetType {
	case RefTargetAsset:
		return mcpScheme + RefTargetAsset + refKeySep + r.AssetID
	case RefTargetPrompt:
		return mcpScheme + RefTargetPrompt + refKeySep + r.PromptID
	case RefTargetCollection:
		return mcpScheme + RefTargetCollection + refKeySep + r.CollectionID
	case RefTargetKnowledgePage:
		return mcpScheme + RefTargetKnowledgePage + refKeySep + r.RefPageID
	case RefTargetConnection:
		return mcpScheme + RefTargetConnection + refKeySep + "(" + r.ConnectionKind + "," + r.ConnectionName + ")"
	case RefTargetDataHub:
		return r.EntityURN
	default:
		return ""
	}
}

// ParseEntityRef parses a serialized reference (the inverse of URN) into a typed
// EntityRef. A "urn:" reference is an external (DataHub) URN, stored verbatim; an
// "mcp:" reference resolves to the matching internal target. Source and the
// owning page id are not part of the serialized form and are left to the caller.
func ParseEntityRef(s string) (EntityRef, error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return EntityRef{}, fmt.Errorf("empty entity reference")
	case strings.HasPrefix(s, externalURNPrefix):
		return EntityRef{TargetType: RefTargetDataHub, EntityURN: s}, nil
	case strings.HasPrefix(s, mcpScheme):
		return parseMCPRef(s)
	default:
		return EntityRef{}, fmt.Errorf("unrecognized entity reference %q (want mcp: or urn:)", s)
	}
}

// parseMCPRef parses the "mcp:<type>:<id>" internal form.
func parseMCPRef(s string) (EntityRef, error) {
	rest := s[len(mcpScheme):]
	typ, id, ok := strings.Cut(rest, refKeySep)
	if !ok || id == "" {
		return EntityRef{}, fmt.Errorf("malformed internal reference %q (want mcp:<type>:<id>)", s)
	}
	if typ == RefTargetConnection {
		kind, name, cErr := parseConnectionTuple(id)
		if cErr != nil {
			return EntityRef{}, fmt.Errorf("invalid connection reference %q: %w", s, cErr)
		}
		return EntityRef{TargetType: RefTargetConnection, ConnectionKind: kind, ConnectionName: name}, nil
	}
	return parseSimpleMCPRef(typ, id, s)
}

// parseSimpleMCPRef parses the single-id internal reference types (everything
// except connection, which carries a (kind,name) pair).
func parseSimpleMCPRef(typ, id, s string) (EntityRef, error) {
	switch typ {
	case RefTargetAsset:
		return EntityRef{TargetType: RefTargetAsset, AssetID: id}, nil
	case RefTargetPrompt:
		// prompt_id is a UUID column; reject a malformed id here so it is a clean
		// client error rather than a database type error surfaced as a 500.
		if _, uErr := uuid.Parse(id); uErr != nil {
			return EntityRef{}, fmt.Errorf("prompt reference id must be a uuid: %q", id)
		}
		return EntityRef{TargetType: RefTargetPrompt, PromptID: id}, nil
	case RefTargetCollection:
		return EntityRef{TargetType: RefTargetCollection, CollectionID: id}, nil
	case RefTargetKnowledgePage:
		return EntityRef{TargetType: RefTargetKnowledgePage, RefPageID: id}, nil
	default:
		return EntityRef{}, fmt.Errorf("unknown internal reference type %q in %q", typ, s)
	}
}

// parseConnectionTuple parses the "(kind,name)" body of a connection reference.
func parseConnectionTuple(body string) (kind, name string, err error) {
	if !strings.HasPrefix(body, "(") || !strings.HasSuffix(body, ")") {
		return "", "", fmt.Errorf("connection reference must be (kind,name)")
	}
	inner := body[1 : len(body)-1]
	kind, name, ok := strings.Cut(inner, ",")
	if !ok || kind == "" || name == "" {
		return "", "", fmt.Errorf("connection reference must be (kind,name)")
	}
	return kind, name, nil
}
