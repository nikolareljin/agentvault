package cmd

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestRoutingModelDownloadWarnsWithoutChecksum(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("model-bytes"))
	}))
	defer srv.Close()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.Flags().String("output", t.TempDir(), "")
	cmd.Flags().String("url", srv.URL, "")
	cmd.Flags().String("sha256", "", "")

	if err := runRoutingModelDownload(cmd, nil); err != nil {
		t.Fatalf("runRoutingModelDownload() error = %v", err)
	}
	if !strings.Contains(out.String(), "warning: no SHA256 provided") {
		t.Fatalf("expected checksum warning in output, got %q", out.String())
	}
}

func TestRoutingModelDownloadSuppressesProgressForRedirectedOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "11")
		_, _ = w.Write([]byte("model-bytes"))
	}))
	defer srv.Close()

	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.Flags().String("output", t.TempDir(), "")
	cmd.Flags().String("url", srv.URL, "")
	cmd.Flags().String("sha256", "", "")

	if err := runRoutingModelDownload(cmd, nil); err != nil {
		t.Fatalf("runRoutingModelDownload() error = %v", err)
	}
	if strings.Contains(out.String(), "\r") {
		t.Fatalf("redirected output should not contain progress carriage returns, got %q", out.String())
	}
}

func TestProgressReaderWritesOnlyWhenEnabled(t *testing.T) {
	quietOut := &bytes.Buffer{}
	quiet := &progressReader{r: strings.NewReader("abc"), total: 3, out: quietOut}
	if _, err := io.ReadAll(quiet); err != nil {
		t.Fatalf("quiet read: %v", err)
	}
	if quietOut.Len() != 0 {
		t.Fatalf("disabled progress wrote output: %q", quietOut.String())
	}

	progressOut := &bytes.Buffer{}
	progress := &progressReader{r: strings.NewReader("abc"), total: 3, out: progressOut, enabled: true}
	if _, err := io.ReadAll(progress); err != nil {
		t.Fatalf("progress read: %v", err)
	}
	if !strings.Contains(progressOut.String(), "\r") {
		t.Fatalf("enabled progress did not write carriage return output: %q", progressOut.String())
	}
}

func TestRoutingModelDownloadRejectsChecksumMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("model-bytes"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	cmd := &cobra.Command{}
	cmd.SetContext(context.Background())
	cmd.SetOut(&bytes.Buffer{})
	cmd.Flags().String("output", dir, "")
	cmd.Flags().String("url", srv.URL, "")
	cmd.Flags().String("sha256", strings.Repeat("0", 64), "")

	err := runRoutingModelDownload(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA256 mismatch") {
		t.Fatalf("expected SHA256 mismatch, got %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Join(dir, "*")); len(matches) != 0 {
		t.Fatalf("expected failed download to leave output dir empty, got %v", matches)
	}
}
