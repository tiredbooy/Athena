package chat

import "testing"

func TestReadToolSchemasEncodeRequiredFieldsAsArrays(t *testing.T) {
	for _, tool := range readToolDefinitions() {
		required, ok := tool.Function.Parameters["required"]
		if !ok {
			continue
		}
		if _, ok := required.([]string); !ok {
			t.Fatalf("%s required schema is %T, want []string", tool.Function.Name, required)
		}
	}
}
