package agent

import "testing"

func TestValidate(t *testing.T) {
	tests := []struct {
		name    string
		agent   Agent
		wantErr bool
	}{
		{
			name:    "valid agent",
			agent:   Agent{Name: "test", Provider: ProviderClaude},
			wantErr: false,
		},
		{
			name:    "missing name",
			agent:   Agent{Provider: ProviderClaude},
			wantErr: true,
		},
		{
			name:    "missing provider",
			agent:   Agent{Name: "test"},
			wantErr: true,
		},
		{
			name:    "unknown provider",
			agent:   Agent{Name: "test", Provider: "unknown"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.agent.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
