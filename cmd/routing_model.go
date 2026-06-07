package cmd

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/nikolareljin/agentvault/internal/localllm"
)

const (
	bitnetModelURL      = "https://huggingface.co/microsoft/bitnet_b1_58-2B-4T-gguf/resolve/main/bitnet_b1_58-2B-4T.gguf"
	bitnetModelFilename = "bitnet_b1_58-2B-4T.gguf"
)

var routingModelCmd = &cobra.Command{
	Use:   "routing-model",
	Short: "Manage the local GGUF model used by llm-router embedded inference",
	Long: `Manage the GGUF model file used by the embedded llama.cpp inference engine.

The embedded engine is only available when agentvault is built with:
  make build-bitnet

The model file (~400 MB) is stored on disk and referenced via:
  --llm-router-model-path PATH
or the llm_router_model_path key in your vault routing config.`,
}

var routingModelStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show embedded inference engine status and model file info",
	Args:  cobra.NoArgs,
	RunE:  runRoutingModelStatus,
}

var routingModelDownloadCmd = &cobra.Command{
	Use:   "download",
	Short: "Download the BitNet-b1.58-2B-4T routing model (~400 MB)",
	Args:  cobra.NoArgs,
	RunE:  runRoutingModelDownload,
}

func init() {
	rootCmd.AddCommand(routingModelCmd)
	routingModelCmd.AddCommand(routingModelStatusCmd)
	routingModelCmd.AddCommand(routingModelDownloadCmd)

	routingModelDownloadCmd.Flags().String("output", "", "directory to save the model (default: ~/.local/share/agentvault/models/)")
	routingModelDownloadCmd.Flags().String("url", bitnetModelURL, "model download URL override")
}

func runRoutingModelStatus(cmd *cobra.Command, _ []string) error {
	_, err := localllm.New("", 0, 0, 0)
	if errors.Is(err, localllm.ErrNotBuilt) {
		fmt.Fprintln(cmd.OutOrStdout(), "embedded inference: disabled (build with 'make build-bitnet' to enable)")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "embedded inference: enabled")
	}

	modelDir := defaultModelDir()
	modelPath := filepath.Join(modelDir, bitnetModelFilename)
	info, err := os.Stat(modelPath)
	if err != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "model file:         not found (run 'agentvault routing-model download')\n")
		fmt.Fprintf(cmd.OutOrStdout(), "expected path:      %s\n", modelPath)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "model file:         %s\n", modelPath)
		fmt.Fprintf(cmd.OutOrStdout(), "model size:         %s\n", formatBytes(info.Size()))
		fmt.Fprintf(cmd.OutOrStdout(), "usage:              --llm-router-model-path %s\n", modelPath)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "OS/arch:            %s/%s\n", runtime.GOOS, runtime.GOARCH)
	return nil
}

func runRoutingModelDownload(cmd *cobra.Command, _ []string) error {
	outputDir, _ := cmd.Flags().GetString("output")
	if outputDir == "" {
		outputDir = defaultModelDir()
	}
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output directory: %w", err)
	}

	modelURL, _ := cmd.Flags().GetString("url")
	destPath := filepath.Join(outputDir, bitnetModelFilename)

	if info, err := os.Stat(destPath); err == nil {
		fmt.Fprintf(cmd.OutOrStdout(), "model already exists: %s (%s)\n", destPath, formatBytes(info.Size()))
		fmt.Fprintln(cmd.OutOrStdout(), "delete the file and re-run to force re-download.")
		return nil
	}

	fmt.Fprintf(cmd.OutOrStdout(), "downloading %s\n  from: %s\n  to:   %s\n\n",
		bitnetModelFilename, modelURL, destPath)

	dlReq, err := http.NewRequestWithContext(cmd.Context(), http.MethodGet, modelURL, nil)
	if err != nil {
		return fmt.Errorf("create download request: %w", err)
	}
	resp, err := http.DefaultClient.Do(dlReq)
	if err != nil {
		return fmt.Errorf("download failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download failed: HTTP %d from %s", resp.StatusCode, modelURL)
	}

	tmp := destPath + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}

	total := resp.ContentLength
	pr := &progressReader{r: resp.Body, total: total, out: cmd.OutOrStdout()}
	if _, err := io.Copy(f, pr); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("write model: %w", err)
	}
	f.Close()
	fmt.Fprintln(cmd.OutOrStdout())

	if err := os.Rename(tmp, destPath); err != nil {
		return fmt.Errorf("finalize model: %w", err)
	}

	info, _ := os.Stat(destPath)
	fmt.Fprintf(cmd.OutOrStdout(), "\ndownloaded: %s (%s)\n", destPath, formatBytes(info.Size()))
	fmt.Fprintf(cmd.OutOrStdout(), "use with:   --llm-router-model-path %s\n", destPath)
	return nil
}

func defaultModelDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "agentvault", "models")
}

func formatBytes(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
		GB = 1024 * MB
	)
	switch {
	case n >= GB:
		return fmt.Sprintf("%.2f GB", float64(n)/GB)
	case n >= MB:
		return fmt.Sprintf("%.2f MB", float64(n)/MB)
	case n >= KB:
		return fmt.Sprintf("%.2f KB", float64(n)/KB)
	default:
		return fmt.Sprintf("%d B", n)
	}
}

type progressReader struct {
	r       io.Reader
	total   int64
	written int64
	out     io.Writer
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.written += int64(n)
	if pr.total > 0 {
		pct := float64(pr.written) / float64(pr.total) * 100
		fmt.Fprintf(pr.out, "\r  %s / %s  (%.1f%%)",
			formatBytes(pr.written), formatBytes(pr.total), pct)
	} else {
		fmt.Fprintf(pr.out, "\r  %s downloaded", formatBytes(pr.written))
	}
	return n, err
}
