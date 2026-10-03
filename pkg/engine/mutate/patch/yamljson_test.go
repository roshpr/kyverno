package patch

import (
	"encoding/json"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	yaml "sigs.k8s.io/kustomize/kyaml/yaml"
)

func TestEscapeYAMLIncompatibleJSON(t *testing.T) {
	t.Parallel()

	ordinary := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"repro"},"spec":{"containers":[{"name":"c","image":"pause"}]}}`
	assert.Equal(t, ordinary, escapeYAMLIncompatibleJSON(ordinary))
	assert.Equal(t, string(baseBytes), escapeYAMLIncompatibleJSON(string(baseBytes)))

	// An escape already in the text must not be rewritten, or the value changes.
	already := `{"value":"\u007f"}`
	assert.Equal(t, already, escapeYAMLIncompatibleJSON(already))

	allowed := []rune{'\u00a0', '😀'}
	for _, r := range allowed {
		in := `{"value":"` + string(r) + `"}`
		assert.Equal(t, in, escapeYAMLIncompatibleJSON(in), "rune %U", r)
	}

	// U+0085 is legal YAML input but is folded to a space inside a JSON string.
	nel := `{"value":"` + "\u0085" + `"}`
	assert.Equal(t, `{"value":"\u0085"}`, escapeYAMLIncompatibleJSON(nel))

	raw := `{"quote":"say \"hi\" ` + "\u007f" + `","keep":"ok"}`
	got := escapeYAMLIncompatibleJSON(raw)
	assert.Equal(t, `{"quote":"say \"hi\" \u007f","keep":"ok"}`, got)
	assert.NotContains(t, got, "\u007f")

	// Keys are JSON strings too.
	assert.Equal(t, `{"a\u007fb":1}`, escapeYAMLIncompatibleJSON("{\"a\u007fb\":1}"))
}

func TestEscapedJSONIsAcceptedByYAML(t *testing.T) {
	t.Parallel()

	value := "a\u007f\u0080\u0085\u009f\ufffe😀"
	raw, err := json.Marshal(map[string]string{"value": value})
	require.NoError(t, err)
	require.Contains(t, string(raw), "\u007f")

	escaped := escapeYAMLIncompatibleJSON(string(raw))
	require.NotContains(t, escaped, "\u007f")

	node, err := yaml.Parse(escaped)
	require.NoError(t, err)
	got, err := node.GetString("value")
	require.NoError(t, err)
	assert.Equal(t, value, got)
}

func TestStrategicMergePatchControlCharacters(t *testing.T) {
	t.Parallel()

	overlay := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]any{
				"foo": "bar",
			},
		},
	}

	tests := []struct {
		name  string
		value string
	}{
		{name: "del", value: "\u007f"},
		{name: "c1-start", value: "\u0080"},
		{name: "nel", value: "\u0085"},
		{name: "c1-end", value: "\u009f"},
		{name: "noncharacter", value: "\ufffe"},
		{name: "mixed", value: "pre\u007fmid\u009fpost"},
		{name: "around-utf8", value: "ok\u007f😀"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resource := map[string]any{
				"apiVersion": "v1",
				"kind":       "Pod",
				"metadata": map[string]any{
					"name": "repro",
				},
				"spec": map[string]any{
					"containers": []any{
						map[string]any{
							"name":  "c",
							"image": "registry.k8s.io/pause:3.10",
							"env": []any{
								map[string]any{"name": "X", "value": tc.value},
							},
						},
					},
				},
			}
			raw, err := json.Marshal(resource)
			require.NoError(t, err)

			out, err := ProcessStrategicMergePatch(logr.Discard(), overlay, raw)
			require.NoError(t, err)

			resource["metadata"].(map[string]any)["labels"] = map[string]any{"foo": "bar"}
			expected, err := json.Marshal(resource)
			require.NoError(t, err)
			assert.JSONEq(t, string(expected), string(out))
		})
	}
}

func TestStrategicMergePatchControlCharacterInOverlay(t *testing.T) {
	t.Parallel()

	resource := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "repro"},
		"spec": map[string]any{
			"containers": []any{
				map[string]any{"name": "c", "image": "registry.k8s.io/pause:3.10"},
			},
		},
	}
	raw, err := json.Marshal(resource)
	require.NoError(t, err)

	overlay := map[string]any{
		"metadata": map[string]any{
			"annotations": map[string]any{
				"desc": "mr\u007f\u009f",
			},
		},
	}
	out, err := ProcessStrategicMergePatch(logr.Discard(), overlay, raw)
	require.NoError(t, err)

	var patched map[string]any
	require.NoError(t, json.Unmarshal(out, &patched))
	annotations := patched["metadata"].(map[string]any)["annotations"].(map[string]any)
	assert.Equal(t, "mr\u007f\u009f", annotations["desc"])
}

func TestStrategicMergePatchAnchorWithControlCharacter(t *testing.T) {
	t.Parallel()

	resource := map[string]any{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]any{"name": "hello"},
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"name":  "hello",
					"image": "gcr.io/google-containers/busybox:latest",
					"env": []any{
						map[string]any{"name": "X", "value": "\u007f"},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(resource)
	require.NoError(t, err)

	matched := map[string]any{
		"spec": map[string]any{
			"containers": []any{
				map[string]any{
					"(image)": "gcr.io/google-containers/busybox:*",
				},
			},
			"imagePullSecrets": []any{
				map[string]any{"name": "regcred"},
			},
		},
	}
	out, err := ProcessStrategicMergePatch(logr.Discard(), matched, raw)
	require.NoError(t, err)

	resource["spec"].(map[string]any)["imagePullSecrets"] = []any{
		map[string]any{"name": "regcred"},
	}
	expected, err := json.Marshal(resource)
	require.NoError(t, err)
	assert.JSONEq(t, string(expected), string(out))
}

func TestStrategicMergePatchInvalidDocumentReturnsError(t *testing.T) {
	t.Parallel()

	_, err := strategicMergePatch(logr.Discard(), "{", `{"metadata":{"labels":{"foo":"bar"}}}`)
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to parse resource")

	_, err = strategicMergePatch(logr.Discard(), `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"repro"}}`, "{")
	require.Error(t, err)
	assert.ErrorContains(t, err, "failed to parse strategic merge overlay")
}
