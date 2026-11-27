package endpointer

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDuplicateJSONKeys(t *testing.T) {
	rules := []AliasRule{
		{OldKey: "team_id", NewKey: "team_id"},
		{OldKey: "team_ids", NewKey: "team_ids"},
		{OldKey: "team_name", NewKey: "team_name"},
	}

	tests := []struct {
		name     string
		input    string
		rules    []AliasRule
		validate func(t *testing.T, result []byte)
	}{
		{
			name:  "BasicDuplication",
			input: `{"team_id": 42, "name": "hello"}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(42), m["team_id"])
				assert.Equal(t, float64(42), m["team_id"])
				assert.Equal(t, "hello", m["name"])
			},
		},
		{
			name:  "NoDuplicationNeeded",
			input: `{"name": "hello", "count": 5}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				assert.JSONEq(t, `{"name": "hello", "count": 5}`, string(result))
			},
		},
		{
			name:  "MultipleRules",
			input: `{"team_id": 1, "team_name": "test"}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(1), m["team_id"])
				assert.Equal(t, float64(1), m["team_id"])
				assert.Equal(t, "test", m["team_name"])
				assert.Equal(t, "test", m["team_name"])
			},
		},
		{
			name:  "NewKeyAlreadyExists",
			input: `{"team_id": 1, "team_id": 2}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				// When new key already exists, no duplication should happen.
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(1), m["team_id"])
				assert.Equal(t, float64(2), m["team_id"])
			},
		},
		{
			name:  "StringValue",
			input: `{"team_name": "my team"}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, "my team", m["team_name"])
				assert.Equal(t, "my team", m["team_name"])
			},
		},
		{
			name:  "NullValue",
			input: `{"team_id": null}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Nil(t, m["team_id"])
				assert.Nil(t, m["team_id"])
				_, hasAutoMDMID := m["team_id"]
				assert.True(t, hasAutoMDMID)
			},
		},
		{
			name:  "BooleanValue",
			input: `{"team_id": true}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, true, m["team_id"])
				assert.Equal(t, true, m["team_id"])
			},
		},
		{
			name:  "ArrayValue",
			input: `{"team_ids": [1, 2, 3]}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, []any{float64(1), float64(2), float64(3)}, m["team_ids"])
				assert.Equal(t, []any{float64(1), float64(2), float64(3)}, m["team_ids"])
			},
		},
		{
			name:  "ObjectValue",
			input: `{"team_id": {"sub": "value"}}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				expected := map[string]any{"sub": "value"}
				assert.Equal(t, expected, m["team_id"])
				assert.Equal(t, expected, m["team_id"])
			},
		},
		{
			name:  "NestedObjects",
			input: `{"outer": {"team_id": 10}}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				inner := m["outer"].(map[string]any)
				assert.Equal(t, float64(10), inner["team_id"])
				assert.Equal(t, float64(10), inner["team_id"])
			},
		},
		{
			name:  "DeeplyNested",
			input: `{"a": {"b": {"c": {"team_id": 99}}}}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				c := m["a"].(map[string]any)["b"].(map[string]any)["c"].(map[string]any)
				assert.Equal(t, float64(99), c["team_id"])
				assert.Equal(t, float64(99), c["team_id"])
			},
		},
		{
			// This simulates the ABM tokens response pattern where a duplicated
			// outer key (e.g., ios_team→ios_team) has an object value that itself
			// contains keys needing duplication (e.g., team_id→team_id).
			// The old-name container keeps both old+new child keys;
			// the new-name container has children renamed to new keys only.
			name:  "DuplicatedKeyWithNestedDuplicatableKeys",
			input: `{"ios_team": {"name": "Default", "team_id": 5}}`,
			rules: []AliasRule{
				{OldKey: "team_id", NewKey: "team_id"},
				{OldKey: "ios_team", NewKey: "ios_team"},
			},
			validate: func(t *testing.T, result []byte) {
				assert.True(t, json.Valid(result), "result should be valid JSON: %s", string(result))
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				// Both ios_team and ios_team should exist.
				iosTeam := m["ios_team"].(map[string]any)
				iosAutoMDM := m["ios_team"].(map[string]any)
				// Old-name container keeps only old child keys.
				assert.Equal(t, "Default", iosTeam["name"])
				assert.Equal(t, float64(5), iosTeam["team_id"])
				_, hasNewKey := iosTeam["team_id"]
				assert.False(t, hasNewKey, "old-name container should not have new child key")
				// New-name container has children renamed to new keys only.
				assert.Equal(t, "Default", iosAutoMDM["name"])
				assert.Equal(t, float64(5), iosAutoMDM["team_id"])
				_, hasOldKey := iosAutoMDM["team_id"]
				assert.False(t, hasOldKey, "new-name container should not have old child key")
			},
		},
		{
			// Three-level rename matching the ABM tokens response:
			// abm_tokens→ab_tokens wraps an array of objects whose
			// macos_team→macos_team containers in turn hold team_id→team_id.
			// The previous release returned both the old- and new-named
			// containers (with clean, internally-consistent leaves) on the same
			// object under abm_tokens; the duplicator must reproduce that while
			// adding the new top-level ab_tokens key.
			name: "MultiLevelRenamedContainers",
			input: `{"abm_tokens":[{"id":1,` +
				`"macos_team":{"name":"T","team_id":22},` +
				`"ios_team":{"name":"T","team_id":22}}]}`,
			rules: []AliasRule{
				{OldKey: "abm_tokens", NewKey: "ab_tokens", Inline: true},
				{OldKey: "macos_team", NewKey: "macos_team"},
				{OldKey: "ios_team", NewKey: "ios_team"},
				{OldKey: "team_id", NewKey: "team_id"},
			},
			validate: func(t *testing.T, result []byte) {
				assert.True(t, json.Valid(result), "result should be valid JSON: %s", string(result))
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))

				// The old top-level key carries BOTH container variants on the
				// same token object, each with clean leaves (no cross id).
				abm := m["abm_tokens"].([]any)
				require.Len(t, abm, 1)
				tok := abm[0].(map[string]any)
				macosTeam := tok["macos_team"].(map[string]any)
				assert.InDelta(t, float64(22), macosTeam["team_id"], 0)
				_, hasAutoMDMID := macosTeam["team_id"]
				assert.False(t, hasAutoMDMID, "macos_team must not be contaminated with team_id")
				macosAutoMDM := tok["macos_team"].(map[string]any)
				assert.InDelta(t, float64(22), macosAutoMDM["team_id"], 0)
				_, hasTeamID := macosAutoMDM["team_id"]
				assert.False(t, hasTeamID, "macos_team must not be contaminated with team_id")

				// The new top-level key is a clean, fully new-named copy.
				ab := m["ab_tokens"].([]any)
				require.Len(t, ab, 1)
				newTok := ab[0].(map[string]any)
				_, hasOldContainer := newTok["macos_team"]
				assert.False(t, hasOldContainer, "ab_tokens token should not contain old-named macos_team")
				newAutoMDM := newTok["macos_team"].(map[string]any)
				assert.InDelta(t, float64(22), newAutoMDM["team_id"], 0)
			},
		},
		{
			// Inline container whose nested renames are LEAVES, not containers
			// (matching the apple_business_manager response, where macos_team is
			// a plain string). The previous release duplicated those leaves in
			// place under the (then-unrenamed) apple_business_manager key, so the
			// inlined old key must carry both leaf names on the same object.
			name: "InlineContainerWithLeafChildren",
			input: `{"apple_business_manager":[` +
				`{"organization_name":"X","macos_team":"T"}]}`,
			rules: []AliasRule{
				{OldKey: "apple_business_manager", NewKey: "apple_business", Inline: true},
				{OldKey: "macos_team", NewKey: "macos_team"},
			},
			validate: func(t *testing.T, result []byte) {
				assert.True(t, json.Valid(result), "result should be valid JSON: %s", string(result))
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))

				// Old key: both leaf names duplicated in place on the same object.
				old := m["apple_business_manager"].([]any)
				require.Len(t, old, 1)
				item := old[0].(map[string]any)
				assert.Equal(t, "T", item["macos_team"])
				assert.Equal(t, "T", item["macos_team"])

				// New key: clean new-named copy only.
				abNew := m["apple_business"].([]any)
				require.Len(t, abNew, 1)
				newItem := abNew[0].(map[string]any)
				assert.Equal(t, "T", newItem["macos_team"])
				_, hasOld := newItem["macos_team"]
				assert.False(t, hasOld, "apple_business item should not contain old-named macos_team")
			},
		},
		{
			name:  "ArrayOfObjects",
			input: `[{"team_id": 1}, {"team_id": 2}]`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var arr []map[string]any
				require.NoError(t, json.Unmarshal(result, &arr))
				require.Len(t, arr, 2)
				assert.Equal(t, float64(1), arr[0]["team_id"])
				assert.Equal(t, float64(1), arr[0]["team_id"])
				assert.Equal(t, float64(2), arr[1]["team_id"])
				assert.Equal(t, float64(2), arr[1]["team_id"])
			},
		},
		{
			name:  "ScopeIsolation",
			input: `{"team_id": 1, "child": {"team_id": 5}}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				// Top level: team_id should be duplicated (no team_id at top level).
				assert.Equal(t, float64(1), m["team_id"])
				assert.Equal(t, float64(1), m["team_id"])
				// Child: team_id exists but team_id doesn't; no duplication
				// (we only duplicate old->new, not new->old).
				child := m["child"].(map[string]any)
				assert.Equal(t, float64(5), child["team_id"])
				_, hasTeamIDInChild := child["team_id"]
				assert.False(t, hasTeamIDInChild)
			},
		},
		{
			name:  "EmptyObject",
			input: `{}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				assert.JSONEq(t, `{}`, string(result))
			},
		},
		{
			name:  "EmptyArray",
			input: `[]`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				assert.JSONEq(t, `[]`, string(result))
			},
		},
		{
			name:  "NoRules",
			input: `{"team_id": 42}`,
			rules: nil,
			validate: func(t *testing.T, result []byte) {
				assert.Equal(t, `{"team_id": 42}`, string(result))
			},
		},
		{
			name:  "EmptyData",
			input: ``,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				assert.Equal(t, ``, string(result))
			},
		},
		{
			name:  "StringValueNotDuplicated",
			input: `{"value": "team_id"}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				// String values that happen to match a key name should NOT trigger duplication.
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, "team_id", m["value"])
				_, hasAutoMDMID := m["team_id"]
				assert.False(t, hasAutoMDMID)
			},
		},
		{
			name:  "NumberWithExponent",
			input: `{"team_id": 1.5e2}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(150), m["team_id"])
				assert.Equal(t, float64(150), m["team_id"])
			},
		},
		{
			name:  "NegativeNumber",
			input: `{"team_id": -7}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(-7), m["team_id"])
				assert.Equal(t, float64(-7), m["team_id"])
			},
		},
		{
			name:  "EscapedQuotesInStringValue",
			input: `{"team_name": "he said \"hi\""}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, `he said "hi"`, m["team_name"])
				assert.Equal(t, `he said "hi"`, m["team_name"])
			},
		},
		{
			name:  "PrettyPrintedJSON",
			input: "{\n  \"team_id\": 42,\n  \"name\": \"test\"\n}",
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(42), m["team_id"])
				assert.Equal(t, float64(42), m["team_id"])
				assert.Equal(t, "test", m["name"])
			},
		},
		{
			name:  "ValidJSON",
			input: `{"team_id": 42, "nested": {"team_name": "x"}, "arr": [{"team_ids": [1]}]}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				// Ensure the result is valid JSON.
				assert.True(t, json.Valid(result), "result should be valid JSON: %s", string(result))

				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(42), m["team_id"])
				assert.Equal(t, float64(42), m["team_id"])

				nested := m["nested"].(map[string]any)
				assert.Equal(t, "x", nested["team_name"])
				assert.Equal(t, "x", nested["team_name"])

				arr := m["arr"].([]any)
				arrObj := arr[0].(map[string]any)
				assert.Equal(t, []any{float64(1)}, arrObj["team_ids"])
				assert.Equal(t, []any{float64(1)}, arrObj["team_ids"])
			},
		},
		{
			name: "LargePayload",
			input: func() string {
				var items []string
				for i := range 100 {
					items = append(items, fmt.Sprintf(`{"team_id": %d, "field_%04d": "val"}`, i, i))
				}
				return "[" + strings.Join(items, ",") + "]"
			}(),
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				assert.True(t, json.Valid(result), "result should be valid JSON")
				var arr []map[string]any
				require.NoError(t, json.Unmarshal(result, &arr))
				require.Len(t, arr, 100)
				for i, obj := range arr {
					assert.Equal(t, float64(i), obj["team_id"])
					assert.Equal(t, float64(i), obj["team_id"])
				}
			},
		},
		{
			name:  "OnlyNewKeyPresent",
			input: `{"team_id": 5}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				// The new key is present but not the old key. We only duplicate
				// old->new, not new->old. So no duplication should happen.
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(5), m["team_id"])
				_, hasTeamID := m["team_id"]
				assert.False(t, hasTeamID)
			},
		},
		{
			name:  "MixedKeysAcrossScopes",
			input: `{"team_id": 1, "child": {"team_id": 2, "team_id": 3}}`,
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				// Top level: team_id duplicated (no team_id at top).
				assert.Equal(t, float64(1), m["team_id"])
				assert.Equal(t, float64(1), m["team_id"])
				// Child: both keys exist, no duplication.
				child := m["child"].(map[string]any)
				assert.Equal(t, float64(2), child["team_id"])
				assert.Equal(t, float64(3), child["team_id"])
			},
		},
		{
			name:  "TrailingNewline",
			input: "{\"team_id\": 1}\n",
			rules: rules,
			validate: func(t *testing.T, result []byte) {
				// json.Encoder appends a newline; ensure it still works.
				var m map[string]any
				require.NoError(t, json.Unmarshal(result, &m))
				assert.Equal(t, float64(1), m["team_id"])
				assert.Equal(t, float64(1), m["team_id"])
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := DuplicateJSONKeys([]byte(tc.input), tc.rules)
			tc.validate(t, result)
		})
	}
}

// TestDuplicateJSONKeysWithEncoder tests that the duplicator works correctly
// with the output of json.Encoder (which adds pretty-printing and a trailing newline).
func TestDuplicateJSONKeysWithEncoder(t *testing.T) {
	rules := []AliasRule{
		{OldKey: "team_id", NewKey: "team_id"},
	}

	type response struct {
		TeamID int    `json:"team_id"` //nolint:apiparamcheck // rename handled centrally by spec.DeprecatedGitOpsKeyMappings
		Name   string `json:"name"`
	}

	data, err := json.MarshalIndent(response{TeamID: 42, Name: "test"}, "", "  ")
	require.NoError(t, err)

	result := DuplicateJSONKeys(data, rules)
	assert.True(t, json.Valid(result), "result should be valid JSON: %s", string(result))

	var m map[string]any
	require.NoError(t, json.Unmarshal(result, &m))
	assert.Equal(t, float64(42), m["team_id"])
	assert.Equal(t, float64(42), m["team_id"])
	assert.Equal(t, "test", m["name"])
}

// TestDuplicateJSONKeysCompact tests that the Compact option disables
// pretty-printing and that the option propagates to recursive calls
// (nested objects whose values are themselves duplicated).
func TestDuplicateJSONKeysCompact(t *testing.T) {
	rules := []AliasRule{
		{OldKey: "team_id", NewKey: "team_id"},
		{OldKey: "ios_team", NewKey: "ios_team"},
	}
	opts := DuplicateJSONKeysOpts{Compact: true}

	t.Run("flat object is compact", func(t *testing.T) {
		input := `{"team_id": 42, "name": "hello"}`
		result := DuplicateJSONKeys([]byte(input), rules, opts)

		// Compact output should have no newlines (other than a possible
		// trailing one from the encoder) or multi-space indentation.
		trimmed := strings.TrimRight(string(result), "\n")
		assert.NotContains(t, trimmed, "\n")
		assert.NotContains(t, trimmed, "  ")

		var m map[string]any
		require.NoError(t, json.Unmarshal(result, &m))
		assert.Equal(t, float64(42), m["team_id"])
		assert.Equal(t, float64(42), m["team_id"])
		assert.Equal(t, "hello", m["name"])
	})

	t.Run("nested duplicated key value is also compact", func(t *testing.T) {
		// ios_team's value contains team_id, which triggers a recursive
		// DuplicateJSONKeys call. The Compact option must propagate so the
		// recursively-processed value is also compact.
		input := `{"ios_team": {"team_id": 5, "name": "Default"}}`
		result := DuplicateJSONKeys([]byte(input), rules, opts)

		trimmed := strings.TrimRight(string(result), "\n")
		assert.NotContains(t, trimmed, "\n")
		assert.NotContains(t, trimmed, "  ")

		var m map[string]any
		require.NoError(t, json.Unmarshal(result, &m))

		// Old-name container keeps only old child keys.
		iosTeam := m["ios_team"].(map[string]any)
		assert.Equal(t, float64(5), iosTeam["team_id"])
		_, hasNewKey := iosTeam["team_id"]
		assert.False(t, hasNewKey)

		// New-name container has children renamed to new keys only.
		iosAutoMDM := m["ios_team"].(map[string]any)
		assert.Equal(t, float64(5), iosAutoMDM["team_id"])
		_, hasOldKey := iosAutoMDM["team_id"]
		assert.False(t, hasOldKey)
	})

	t.Run("default (no opts) is indented", func(t *testing.T) {
		input := `{"team_id": 42}`
		expected := `{
  "team_id": 42,
  "team_id": 42
}
`
		result := DuplicateJSONKeys([]byte(input), rules)
		assert.Equal(t, expected, string(result))
	})
}

// TestDuplicateJSONKeysIdempotent ensures that running the duplicator twice
// doesn't add more keys (since after the first run the new key exists).
func TestDuplicateJSONKeysIdempotent(t *testing.T) {
	rules := []AliasRule{
		{OldKey: "team_id", NewKey: "team_id"},
	}

	input := `{"team_id": 42}`
	expected := `{
  "team_id": 42,
  "team_id": 42
}
`

	first := DuplicateJSONKeys([]byte(input), rules)

	assert.Equal(t, expected, string(first))

	// Second pass should not add anything new.
	second := DuplicateJSONKeys(first, rules)
	assert.Equal(t, expected, string(second))
}

// TestDuplicateJSONKeysScopedRule is the response-side counterpart of TestJSONKeyRewriteReader_ScopedRule. The first
// case is the real AutoMDM shape: the renamed `macos_setup` container still splits into an all-new `setup_experience`
// sibling, while the same leaf name under `windows_settings` must not gain a second spelling. The rest pin how a scope
// resolves against the enclosing object: array elements answer to the array's own key, the document root matches no
// scope at all, and a scoped rule beats an unscoped one sharing its name so declaration order can't decide which wins.
func TestDuplicateJSONKeysScopedRule(t *testing.T) {
	automdmKeys := []AliasRule{
		{OldKey: "macos_setup", NewKey: "setup_experience"},
		{
			OldKey: "enable_managed_local_account",
			NewKey: "enable_create_local_admin_account",
			Scope:  []string{"macos_setup", "setup_experience"},
		},
	}
	arrayScoped := []AliasRule{{OldKey: "macos_team", NewKey: "macos_team", Scope: []string{"abm_tokens"}}}
	parentScoped := []AliasRule{{OldKey: "k", NewKey: "k2", Scope: []string{"parent"}}}
	tie := []AliasRule{
		{OldKey: "k", NewKey: "unscoped"},
		{OldKey: "k", NewKey: "scoped", Scope: []string{"parent"}},
	}
	inlined := []AliasRule{
		{OldKey: "abm_tokens", NewKey: "ab_tokens", Inline: true},
		{OldKey: "macos_team", NewKey: "macos_team", Scope: []string{"abm_tokens", "ab_tokens"}},
	}

	cases := []struct {
		name  string
		rules []AliasRule
		input string
		want  string
	}{
		{
			name:  "the Windows toggle keeps its only name while macos_setup splits",
			rules: automdmKeys,
			input: `{"mdm": {
				"macos_setup": {"enable_managed_local_account": true},
				"windows_settings": {"enable_managed_local_account": false}
			}}`,
			want: `{"mdm": {
				"macos_setup": {"enable_managed_local_account": true},
				"setup_experience": {"enable_create_local_admin_account": true},
				"windows_settings": {"enable_managed_local_account": false}
			}}`,
		},
		{
			name:  "array elements inherit the array's key",
			rules: arrayScoped,
			input: `{"abm_tokens":[{"macos_team":"a"}]}`,
			want:  `{"abm_tokens":[{"macos_team":"a","macos_team":"a"}]}`,
		},
		{
			name:  "an array under another key is out of scope",
			rules: arrayScoped,
			input: `{"other":[{"macos_team":"a"}]}`,
			want:  `{"other":[{"macos_team":"a"}]}`,
		},
		{
			name:  "the document root matches no scope",
			rules: parentScoped,
			input: `{"k":1}`,
			want:  `{"k":1}`,
		},
		{
			name:  "a new key already present in scope suppresses the duplicate",
			rules: parentScoped,
			input: `{"parent":{"k":1,"k2":9}}`,
			want:  `{"parent":{"k":1,"k2":9}}`,
		},
		{
			name:  "a scoped rule wins over an unscoped one inside its scope",
			rules: tie,
			input: `{"parent":{"k":1}}`,
			want:  `{"parent":{"k":1,"scoped":1}}`,
		},
		{
			name:  "the unscoped rule still applies outside that scope",
			rules: tie,
			input: `{"elsewhere":{"k":1}}`,
			want:  `{"elsewhere":{"k":1,"unscoped":1}}`,
		},
		{
			name:  "an inlined container carries its scoped leaf under both names",
			rules: inlined,
			input: `{"abm_tokens":[{"macos_team":"a"}]}`,
			want:  `{"abm_tokens":[{"macos_team":"a","macos_team":"a"}],"ab_tokens":[{"macos_team":"a"}]}`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.JSONEq(t, tc.want, string(DuplicateJSONKeys([]byte(tc.input), tc.rules, DuplicateJSONKeysOpts{Compact: true})))
			// the indent option controls formatting only, never which keys are emitted
			assert.JSONEq(t, tc.want, string(DuplicateJSONKeys([]byte(tc.input), tc.rules)))
		})
	}
}
