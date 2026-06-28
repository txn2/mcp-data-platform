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
	case RefTargetInsight:
		return mcpScheme + RefTargetInsight + refKeySep + r.InsightID
	case RefTargetMemory:
		return mcpScheme + RefTargetMemory + refKeySep + r.MemoryID
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
		// A DataHub URN never embeds the internal mcp: scheme; if it does, the
		// caller crossed the two namespaces (e.g. urn:li:mcp:connection:(x)). Reject
		// it rather than store a reference that resolves to nothing.
		if strings.Contains(s, mcpScheme) {
			return EntityRef{}, fmt.Errorf("reference %q mixes the urn: and mcp: schemes; use %s<type>:<key> for an internal entity (asset, prompt, collection, knowledge_page, connection) or a plain urn:li:... for a DataHub entity", s, mcpScheme)
		}
		return EntityRef{TargetType: RefTargetDataHub, EntityURN: s}, nil
	case strings.HasPrefix(s, mcpScheme):
		return parseMCPRef(s)
	default:
		return EntityRef{}, fmt.Errorf("unrecognized entity reference %q (want mcp: or urn:)", s)
	}
}

// ParseCitableRef parses a reference for attachment to a knowledge page. It is
// ParseEntityRef plus the page-citation policy: a reference type that is fetchable
// but not citable on a shared page is rejected here, even though it parses and
// dereferences. Today those are the per-user sources, personal memory
// (mcp:memory:<id>) and captured insights (mcp:insight:<id>): both are ScopePerUser,
// so a citation embedded in a shared page would resolve only for its owner and be a
// broken citation for everyone else (#699). An insight that is promoted to the
// catalog via apply_knowledge becomes a shared DataHub entity, which IS citable as
// its urn:li:... form. Use this on the page-authoring paths (apply_knowledge
// references, the REST picker, the inline body scan); fetch keeps using
// ParseEntityRef so both forms remain fetchable by their owner.
func ParseCitableRef(s string) (EntityRef, error) {
	ref, err := ParseEntityRef(s)
	if err != nil {
		return EntityRef{}, err
	}
	if ref.TargetType == RefTargetMemory || ref.TargetType == RefTargetInsight {
		return EntityRef{}, fmt.Errorf("a personal %s reference (%q) cannot be cited on a knowledge page: it is private to its owner, so the citation would resolve for no one else; promote the insight to the catalog and cite the resulting urn:li:... entity, or cite a shared entity instead", ref.TargetType, s)
	}
	return ref, nil
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
	case RefTargetInsight:
		// Insight and memory ids are opaque memory_records ids (not bare UUIDs with a
		// type column to validate against), so they are accepted as-is; the owner-scoped
		// fetch resolves them and reports a stale id as not-found.
		return EntityRef{TargetType: RefTargetInsight, InsightID: id}, nil
	case RefTargetMemory:
		return EntityRef{TargetType: RefTargetMemory, MemoryID: id}, nil
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
