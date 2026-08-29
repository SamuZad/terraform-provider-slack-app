package provider

import "encoding/json"

func setIfAbsent(m map[string]interface{}, key string, value interface{}) {
	if _, exists := m[key]; !exists {
		m[key] = value
	}
}

// applyManifestDefaults fills in the server-side defaults Slack applies to
// app manifests, observed from apps.manifest.export: an exported manifest
// carries these attributes even when the authored manifest omitted them.
// Normalizing both sides before comparison keeps omitted-in-config attributes
// from producing permanent diffs, while still surfacing real drift (a value
// changed away from its default shows up, because the authored side
// normalizes to the default).
func applyManifestDefaults(v interface{}) interface{} {
	m, ok := v.(map[string]interface{})
	if !ok {
		return v
	}
	settings, ok := m["settings"].(map[string]interface{})
	if !ok {
		settings = map[string]interface{}{}
		m["settings"] = settings
	}
	setIfAbsent(settings, "org_deploy_enabled", false)
	setIfAbsent(settings, "socket_mode_enabled", false)
	setIfAbsent(settings, "is_mcp_enabled", false)
	setIfAbsent(settings, "token_rotation_enabled", false)
	if features, ok := m["features"].(map[string]interface{}); ok {
		if botUser, ok := features["bot_user"].(map[string]interface{}); ok {
			setIfAbsent(botUser, "always_online", true)
		}
	}
	if oauth, ok := m["oauth_config"].(map[string]interface{}); ok {
		setIfAbsent(oauth, "pkce_enabled", false)
	}
	return m
}

// normalizeManifest fills Slack's server-side defaults into an authored
// manifest before it is sent to the API. This makes removing an attribute
// from config an explicit reset to its default value, instead of relying on
// how Slack treats keys omitted from an update.
func normalizeManifest(manifest string) string {
	var v interface{}
	if err := json.Unmarshal([]byte(manifest), &v); err != nil {
		return manifest // invalid JSON: let the API report it
	}
	normalized, err := json.Marshal(applyManifestDefaults(v))
	if err != nil {
		return manifest
	}
	return string(normalized)
}

// jsonSemanticallyEqual reports whether two manifests are equal after
// normalization: Slack's server-side defaults are applied to both sides, and
// object key order, whitespace, and array element order are ignored (Slack
// treats manifest arrays such as scopes as sets).
func jsonSemanticallyEqual(a, b string) bool {
	var av, bv interface{}
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return false
	}
	return jsonValueEqual(applyManifestDefaults(av), applyManifestDefaults(bv))
}

func jsonValueEqual(a, b interface{}) bool {
	switch at := a.(type) {
	case map[string]interface{}:
		bt, ok := b.(map[string]interface{})
		if !ok || len(at) != len(bt) {
			return false
		}
		for key, value := range at {
			other, ok := bt[key]
			if !ok || !jsonValueEqual(value, other) {
				return false
			}
		}
		return true
	case []interface{}:
		bt, ok := b.([]interface{})
		if !ok || len(at) != len(bt) {
			return false
		}
		// Order-insensitive multiset match.
		used := make([]bool, len(bt))
		for _, value := range at {
			found := false
			for i, other := range bt {
				if !used[i] && jsonValueEqual(value, other) {
					used[i] = true
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	default:
		// Scalars: string, float64, bool, nil.
		return a == b
	}
}
