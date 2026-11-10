package cmd

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/nikolareljin/agentvault/internal/memory"
)

func runMemory(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&out)
	rootCmd.SetArgs(append([]string{"memory", "serve"}, args...))
	defer func() {
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
		rootCmd.SetArgs(nil)
		if fl := memoryServeCmd.Flags().Lookup("port"); fl != nil {
			_ = fl.Value.Set(fl.DefValue)
			fl.Changed = false
		}
		for _, name := range []string{"describe", "db"} {
			if fl := memoryServeCmd.Flags().Lookup(name); fl != nil {
				_ = fl.Value.Set(fl.DefValue)
				fl.Changed = false
			}
		}
	}()
	err := rootCmd.Execute()
	return out.String(), err
}

func TestMemoryServeDescribe(t *testing.T) {
	t.Setenv("AGENTVAULT_MEMORY_PORT", "")
	out, err := runMemory(t, "--describe")
	if err != nil {
		t.Fatalf("--describe: %v\n%s", err, out)
	}
	var d memory.Descriptor
	if err := json.Unmarshal([]byte(out), &d); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, out)
	}
	if d.Port != memory.DefaultPort || d.Bind != "127.0.0.1" || len(d.Routes) != 4 {
		t.Fatalf("descriptor %+v", d)
	}
	out, err = runMemory(t, "--describe", "--port", "9999")
	if err != nil || !strings.Contains(out, `"port": 9999`) {
		t.Fatalf("--port 9999: %v\n%s", err, out)
	}
	t.Setenv("AGENTVAULT_MEMORY_PORT", "9123")
	out, err = runMemory(t, "--describe")
	if err != nil || !strings.Contains(out, `"port": 9123`) || !strings.Contains(out, `"localhost:9123"`) {
		t.Fatalf("env port: %v\n%s", err, out)
	}
	// --port 0 is an ephemeral port; the descriptor shows the default.
	out, err = runMemory(t, "--describe", "--port", "0")
	if err != nil || !strings.Contains(out, `"port": 9030`) {
		t.Fatalf("--port 0: %v\n%s", err, out)
	}
}

func TestMemoryServeRefusesBadConfig(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		args []string
		want string
	}{
		{"host not loopback", map[string]string{"AGENTVAULT_MEMORY_HOST": "0.0.0.0"}, nil, "binds 127.0.0.1 only"},
		{"host name", map[string]string{"AGENTVAULT_MEMORY_HOST": "localhost"}, nil, "binds 127.0.0.1 only"},
		{"host empty", map[string]string{"AGENTVAULT_MEMORY_HOST": ""}, nil, "binds 127.0.0.1 only"},
		{"port text", map[string]string{"AGENTVAULT_MEMORY_PORT": "nine"}, nil, "not a port number"},
		{"port high", map[string]string{"AGENTVAULT_MEMORY_PORT": "65536"}, nil, "outside 1-65535"},
		{"flag negative", nil, []string{"--port", "-1"}, "outside 1-65535"},
		{"flag high", nil, []string{"--port", "70000"}, "outside 1-65535"},
		{"positional", nil, []string{"extra"}, "unknown command"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			out, err := runMemory(t, append([]string{"--describe"}, tc.args...)...)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q\n%s", err, tc.want, out)
			}
		})
	}
	// The loopback value itself is accepted.
	t.Setenv("AGENTVAULT_MEMORY_HOST", "127.0.0.1")
	if _, err := runMemory(t, "--describe"); err != nil {
		t.Fatal(err)
	}
}
