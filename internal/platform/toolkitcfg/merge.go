package toolkitcfg

import "log/slog"

// Config schema key for the per-kind enable flag.
const keyEnabled = "enabled"

// AutoEnableKind ensures toolkits[kind] exists with enabled=true so the toolkit
// loader will instantiate it. Idempotent and non-overriding: if the operator has
// already declared the kind block (enabled OR disabled), their explicit choice
// is respected.
//
// Logs at Debug, not Info: this is the platform's documented default behavior,
// not an exceptional condition that requires operator attention. Operators who
// want to silence the path entirely can set the kind explicitly in YAML (with
// either enabled state).
func AutoEnableKind(toolkits map[string]any, kind string) {
	if _, exists := toolkits[kind]; exists {
		return
	}
	toolkits[kind] = map[string]any{keyEnabled: true}
	slog.Debug("auto-enabled toolkit kind (requirements met, no explicit YAML)",
		"kind", kind)
}

// MergeInstance merges one stored connection into the toolkit config map under
// its kind's instances. It is a no-op when the kind is absent or disabled, and
// when the kind already carries an instance of that name: file config takes
// precedence over a connection held in the database.
//
// Instances merged here arrive after Config.Validate has run, so they are not
// covered by MissingDefaults: a kind can hold several of them with no
// "default", which is why ResolveDefaultInstance resolves deterministically
// rather than relying on that refusal.
func MergeInstance(toolkits map[string]any, kind, name string, cfg map[string]any) {
	kindMap, ok := toolkits[kind].(map[string]any)
	if !ok || !KindEnabled(kindMap) {
		return
	}

	kindInstances, ok := kindMap[keyInstances].(map[string]any)
	if !ok {
		kindInstances = make(map[string]any)
		kindMap[keyInstances] = kindInstances
	}

	if _, exists := kindInstances[name]; !exists {
		kindInstances[name] = cfg
		slog.Info("merged DB connection into toolkit config", "kind", kind, "name", name)
	}
}

// PinDeclaredDefaults records, for every kind that declares instances without a
// "default", the instance its config resolves to today. Call it once before
// merging connections held in the database.
//
// Without it, a kind that declares a single instance and needs no "default"
// changes meaning when an admin-UI connection whose name sorts earlier joins
// the same map: the next restart resolves the unqualified lookup to the new
// connection and moves a provider, or managed-resource blob storage, off the
// connection the file pointed at. Pinning first is the same rule MergeInstance
// already follows, that file config outranks a stored connection.
func PinDeclaredDefaults(toolkits map[string]any) {
	for _, kindCfg := range toolkits {
		kindMap, ok := kindCfg.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := kindMap[keyDefault].(string); ok && name != "" {
			continue
		}
		instances, ok := kindMap[keyInstances].(map[string]any)
		if !ok || len(instances) == 0 {
			continue
		}
		kindMap[keyDefault] = ResolveDefaultInstance(kindMap, instances)
	}
}

// KindEnabled reports whether a toolkit kind map has enabled=true. It handles
// both bool and string values, because environment-variable expansion produces
// strings.
func KindEnabled(kindMap map[string]any) bool {
	v, ok := kindMap[keyEnabled]
	if !ok {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val == "true"
	default:
		return false
	}
}
