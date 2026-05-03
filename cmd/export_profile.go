package cmd

import (
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
// format must be "json", "yaml", or "" (defaults to JSON).
func marshalAgentProfile(a agent.Agent, format string) ([]byte, error) {
	switch format {
	case "", "json", "yaml":
	default:
		return nil, fmt.Errorf("unknown format %q; use json or yaml", format)
	}
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
// format must be "json", "yaml", or "" (autodetect).
func unmarshalAgentProfile(data []byte, format string) (agent.Agent, string, error) {
	switch format {
	case "", "json", "yaml":
	default:
		return agent.Agent{}, "", fmt.Errorf("unknown format %q; use json or yaml", format)
	}
	var export AgentProfileExport
	var err error
	if format == "json" {
		if err = json.Unmarshal(data, &export); err != nil {
			return agent.Agent{}, "", fmt.Errorf("decoding profile JSON: %w", err)
		}
	} else if format == "yaml" {
		if export, err = unmarshalYAML[AgentProfileExport](data); err != nil {
			return agent.Agent{}, "", err
		}
	} else {
		// Autodetect: try JSON first; YAML flow-mappings also start with '{'
		// so byte-sniffing is unreliable.
		if err = json.Unmarshal(data, &export); err != nil {
			if export, err = unmarshalYAML[AgentProfileExport](data); err != nil {
				return agent.Agent{}, "", err
			}
		}
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
