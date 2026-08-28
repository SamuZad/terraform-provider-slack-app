package provider

import "testing"

func TestJSONSemanticallyEqual(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"array order", `{"scopes":["a","b","c"]}`, `{"scopes":["c","a","b"]}`, true},
		{"key order and whitespace", `{"x":1,"y":2}`, ` {"y": 2, "x": 1} `, true},
		{"nested arrays", `{"o":{"s":{"bot":["chat:write","channels:read"]}}}`, `{"o":{"s":{"bot":["channels:read","chat:write"]}}}`, true},
		{"real difference", `{"scopes":["a","b"]}`, `{"scopes":["a","c"]}`, false},
		{"duplicate elements respected", `{"s":["a","a","b"]}`, `{"s":["a","b","b"]}`, false},
		{"length mismatch", `{"s":["a"]}`, `{"s":["a","a"]}`, false},
		{"scalar types", `{"n":1,"b":true,"z":null}`, `{"b":true,"z":null,"n":1}`, true},
		{"number vs string", `{"n":1}`, `{"n":"1"}`, false},
		{"invalid json", `notjson`, `notjson`, false},
	}
	for _, c := range cases {
		if got := jsonSemanticallyEqual(c.a, c.b); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}
