package cmd

import (
	"encoding/json"
	"testing"
)

func TestSetupBundleMarshalIncludesWorkflowTemplatesField(t *testing.T) {
	data, err := json.Marshal(SetupBundle{})
	if err != nil {
		t.Fatalf("json.Marshal(SetupBundle{}) error = %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("json.Unmarshal(marshal output) error = %v", err)
	}
	if _, ok := payload["workflow_templates"]; !ok {
		t.Fatalf("marshal output keys = %v, want workflow_templates field present", payload)
	}
}
