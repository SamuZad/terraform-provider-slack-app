package provider

import "encoding/json"

// jsonSemanticallyEqual reports whether two JSON documents are equal ignoring
// object key order, whitespace, and array element order. Slack treats manifest
// arrays (scopes, event types, ...) as sets and returns them in its own order,
// so comparing them positionally would produce permanent diffs.
func jsonSemanticallyEqual(a, b string) bool {
	var av, bv interface{}
	if err := json.Unmarshal([]byte(a), &av); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(b), &bv); err != nil {
		return false
	}
	return jsonValueEqual(av, bv)
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
