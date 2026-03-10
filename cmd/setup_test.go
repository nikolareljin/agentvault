package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSetupBundleMarshalIncludesWorkflowTemplatesField(t *testing.T) {
	data, err := json.Marshal(SetupBundle{})
	if err != nil {
		t.Fatalf("json.Marshal(SetupBundle{}) error = %v", err)
	}
	if !strings.Contains(string(data), `"workflow_templates"`) {
		t.Fatalf("marshal output = %s, want workflow_templates field present", string(data))
	}
}
