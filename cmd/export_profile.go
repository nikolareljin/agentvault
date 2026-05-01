package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"gopkg.in/yaml.v3"
)

const agentProfileSchemaVersion = "1.0"

// AgentProfileExport is the portable schema for a single-agent profile.
// It is a presentation type used only for export/import; Agent is the canonical store type.
type AgentProfileExport struct {
	SchemaVersion string      `json:"schema_version" yaml:"schema_version"`
	ExportedAt    time.Time   `json:"exported_at"    yaml:"exported_at"`
	Agent         agent.Agent `json:"agent"          yaml:"agent"`
}

// marshalAgentProfile serializes an agent profile to JSON or YAML.
func marshalAgentProfile(a agent.Agent, format string) ([]byte, error) {
	export := AgentProfileExport{
		SchemaVersion: agentProfileSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Agent:         a,
	}
	if format == "yaml" {
		return marshalYAML(export)
	}
	return marshalJSON(export)
}

// unmarshalAgentProfile parses a JSON or YAML agent profile export.
// Format is autodetected from the first byte when not specified.
func unmarshalAgentProfile(data []byte, format string) (agent.Agent, string, error) {
	if format == "" {
		trimmed := bytes.TrimSpace(data)
		if len(trimmed) > 0 && trimmed[0] == '{' {
			format = "json"
		} else {
			format = "yaml"
		}
	}

	var export AgentProfileExport
	var err error
	if format == "yaml" {
		export, err = unmarshalYAML[AgentProfileExport](data)
	} else {
		if err2 := json.Unmarshal(data, &export); err2 != nil {
			err = fmt.Errorf("decoding profile JSON: %w", err2)
		}
	}
	if err != nil {
		return agent.Agent{}, "", err
	}
	return export.Agent, export.SchemaVersion, nil
}

// marshalJSON serializes v to indented JSON.
func marshalJSON(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encoding JSON: %w", err)
	}
	return data, nil
}

// marshalYAML serializes v to YAML.
func marshalYAML(v any) ([]byte, error) {
	data, err := yaml.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encoding YAML: %w", err)
	}
	return data, nil
}

// unmarshalYAML deserializes YAML data into T.
func unmarshalYAML[T any](data []byte) (T, error) {
	var v T
	if err := yaml.Unmarshal(data, &v); err != nil {
		return v, fmt.Errorf("decoding YAML: %w", err)
	}
	return v, nil
}

// unmarshalJSONSlice deserializes a JSON array into []T.
func unmarshalJSONSlice[T any](data []byte) ([]T, error) {
	var v []T
	if err := json.Unmarshal(data, &v); err != nil {
		return nil, fmt.Errorf("decoding JSON: %w", err)
	}
	return v, nil
}
