package cmd

import (
	"encoding/json"
	"io"
	"os"
	"testing"

	"github.com/spf13/cobra"
)

func TestStatusNoVaultCostReportOmitsUnavailableCost(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.Flags().Bool("json", true, "")
	cmd.Flags().Bool("no-vault", true, "")
	cmd.Flags().String("vault-password-env", "AGENTVAULT_PASSWORD", "")
	cmd.Flags().Bool("cost-report", true, "")

	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w
	t.Cleanup(func() { os.Stdout = oldStdout })

	runErr := runStatus(cmd, nil)
	_ = w.Close()
	out, readErr := io.ReadAll(r)
	if readErr != nil {
		t.Fatalf("read stdout: %v", readErr)
	}
	if runErr != nil {
		t.Fatalf("runStatus() error = %v", runErr)
	}

	var payload map[string]any
	if err := json.Unmarshal(out, &payload); err != nil {
		t.Fatalf("unmarshal status JSON: %v\n%s", err, out)
	}
	if _, ok := payload["cost"]; ok {
		t.Fatalf("cost should be omitted when --no-vault leaves history unavailable, got %v", payload["cost"])
	}
}
