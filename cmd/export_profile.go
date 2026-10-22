package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nikolareljin/agentvault/internal/agent"
	"gopkg.in/yaml.v3"
)

const agentProfileSchemaVersion = "1.0"

// blankedAgentProfileSchemaVersion marks a profile whose secrets export
// blanked in place. The agent goes under "blanked_agent", so 0.14.0 and
// earlier, which would write the blanks over this machine's values on
// --merge, find no agent and refuse the file.
const blankedAgentProfileSchemaVersion = "1.1"

// AgentProfileExport is the portable schema for a single-agent profile.
// It is a presentation type used only for export/import; Agent is the canonical store type.
type AgentProfileExport struct {
	SchemaVersion        string       `json:"schema_version,omitempty"         yaml:"schema_version,omitempty"`
	BlankedSchemaVersion string       `json:"blanked_schema_version,omitempty" yaml:"blanked_schema_version,omitempty"`
	ExportedAt           time.Time    `json:"exported_at"                      yaml:"exported_at"`
	Agent                *agent.Agent `json:"agent,omitempty"                  yaml:"agent,omitempty"`
	BlankedAgent         *agent.Agent `json:"blanked_agent,omitempty"          yaml:"blanked_agent,omitempty"`
	// Blanks are the base URL and extras export blanked (as vault_blanks
	// names them), so import can name the ones it cannot fill.
	Blanks []string `json:"blanks,omitempty" yaml:"blanks,omitempty"`
}

// marshalAgentProfile serializes an agent profile to JSON or YAML.
// format must be "json", "yaml", or "" (defaults to JSON).
func marshalAgentProfile(a agent.Agent, format string) ([]byte, error) {
	switch format {
	case "", "json", "yaml":
	default:
		return nil, fmt.Errorf("unknown format %q; use json or yaml", format)
	}
	return encodeAgentProfile(AgentProfileExport{
		SchemaVersion: agentProfileSchemaVersion,
		ExportedAt:    time.Now().UTC(),
		Agent:         &a,
	}, format)
}

// marshalBlankedAgentProfile serializes a profile whose secrets export
// blanked; blanks names the blanked base URL and extras.
func marshalBlankedAgentProfile(a agent.Agent, blanks []string, format string) ([]byte, error) {
	switch format {
	case "", "json", "yaml":
	default:
		return nil, fmt.Errorf("unknown format %q; use json or yaml", format)
	}
	return encodeAgentProfile(AgentProfileExport{
		BlankedSchemaVersion: blankedAgentProfileSchemaVersion,
		ExportedAt:           time.Now().UTC(),
		BlankedAgent:         &a,
		Blanks:               blanks,
	}, format)
}

func encodeAgentProfile(export AgentProfileExport, format string) ([]byte, error) {
	if format == "yaml" {
		return marshalYAML(export)
	}
	return marshalJSON(export)
}

// unmarshalAgentProfile parses a JSON or YAML agent profile export.
// Format is autodetected by attempting JSON unmarshal first and falling back to YAML.
// format must be "json", "yaml", or "" (autodetect).
func unmarshalAgentProfile(data []byte, format string) (agent.Agent, string, error) {
	a, version, _, err := decodeAgentProfile(data, format)
	return a, version, err
}

// decodeAgentProfile is unmarshalAgentProfile that also returns the
// blanks a blanked profile names.
func decodeAgentProfile(data []byte, format string) (agent.Agent, string, []string, error) {
	switch format {
	case "", "json", "yaml":
	default:
		return agent.Agent{}, "", nil, fmt.Errorf("unknown format %q; use json or yaml", format)
	}
	var export AgentProfileExport
	var err error
	if format == "json" {
		if err = json.Unmarshal(data, &export); err != nil {
			return agent.Agent{}, "", nil, fmt.Errorf("decoding profile JSON: %w", err)
		}
	} else if format == "yaml" {
		if export, err = unmarshalYAML[AgentProfileExport](data); err != nil {
			return agent.Agent{}, "", nil, err
		}
	} else {
		// Autodetect: try JSON first; YAML flow-mappings also start with '{'
		// so byte-sniffing is unreliable.
		if err = json.Unmarshal(data, &export); err != nil {
			if export, err = unmarshalYAML[AgentProfileExport](data); err != nil {
				return agent.Agent{}, "", nil, err
			}
		}
	}
	blanked := export.BlankedSchemaVersion != "" || export.BlankedAgent != nil || len(export.Blanks) > 0
	if !blanked {
		var a agent.Agent
		if export.Agent != nil {
			a = *export.Agent
		}
		return a, export.SchemaVersion, nil, nil
	}
	// A blanked profile has only the blanked form: anything else was edited.
	if export.Agent != nil || export.SchemaVersion != "" || export.BlankedAgent == nil {
		return agent.Agent{}, "", nil, errors.New("agent profile mixes the blanked and the plain layout; it was edited and is not trusted")
	}
	if export.BlankedSchemaVersion != blankedAgentProfileSchemaVersion {
		return agent.Agent{}, "", nil, fmt.Errorf("agent profile version %q is not one this release reads (%s, %s); upgrade agentvault",
			export.BlankedSchemaVersion, agentProfileSchemaVersion, blankedAgentProfileSchemaVersion)
	}
	return *export.BlankedAgent, agentProfileSchemaVersion, export.Blanks, nil
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
