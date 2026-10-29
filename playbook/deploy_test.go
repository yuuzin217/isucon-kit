package playbook

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestDeployHealthCheck runs the real deploy template with a real curl, but
// stubs build/restart and the retry delay so it cannot touch a service.
func TestDeployHealthCheck(t *testing.T) {
	curlPath, err := exec.LookPath("curl")
	if err != nil {
		t.Fatal("curl is required for deploy smoke tests:", err)
	}

	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()

	refusedListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refusedURL := "http://" + refusedListener.Addr().String() + "/"
	if err := refusedListener.Close(); err != nil {
		t.Fatal(err)
	}

	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // curl must time out and close this request.
	}))
	defer stalled.Close()

	for _, tc := range []struct {
		name       string
		url        string
		wantExit   int
		wantOutput string
	}{
		{"healthy", healthy.URL, 0, "[deploy] OK"},
		{"connection_refused", refusedURL, 1, "[deploy] FAILED"},
		{"unresponsive_server", stalled.URL, 1, "[deploy] FAILED"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			appDir := t.TempDir()
			binDir := filepath.Join(t.TempDir(), "bin")
			if err := os.Mkdir(binDir, 0700); err != nil {
				t.Fatal(err)
			}

			// No real build, sudo, service restart, or retry sleep is allowed.
			shims := map[string]string{
				"go":    "#!/bin/sh\n[ \"$1\" = build ] || exit 20\n",
				"sudo":  "#!/bin/sh\n[ \"$1\" = systemctl ] && [ \"$2\" = restart ] && [ \"$3\" = isucon ] || exit 21\n",
				"seq":   "#!/bin/sh\nprintf '1\\n'\n", // One attempt per case.
				"sleep": "#!/bin/sh\nexit 0\n",
				"curl":  "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$CURL_ARGS_FILE\"\nexec \"$REAL_CURL\" \"$@\"\n",
			}
			for name, content := range shims {
				if err := os.WriteFile(filepath.Join(binDir, name), []byte(content), 0700); err != nil {
					t.Fatal(err)
				}
			}

			template, err := os.ReadFile("deploy.sh")
			if err != nil {
				t.Fatal(err)
			}
			script := string(template)
			for _, replacement := range [][2]string{
				{"APP_DIR=\"/home/isucon/webapp/go\"", fmt.Sprintf("APP_DIR=%q", appDir)},
				{"HEALTH_URL=\"http://127.0.0.1:8080/\"", fmt.Sprintf("HEALTH_URL=%q", tc.url)},
			} {
				if !strings.Contains(script, replacement[0]) {
					t.Fatalf("deploy template changed: missing %s", replacement[0])
				}
				script = strings.Replace(script, replacement[0], replacement[1], 1)
			}
			scriptPath := filepath.Join(t.TempDir(), "deploy.sh")
			if err := os.WriteFile(scriptPath, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}

			argsFile := filepath.Join(t.TempDir(), "curl.args")
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "bash", scriptPath)
			cmd.Env = append(os.Environ(),
				"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"REAL_CURL="+curlPath,
				"CURL_ARGS_FILE="+argsFile,
			)
			start := time.Now()
			output, runErr := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("deploy never completed within 12s (elapsed %v): %s: %v", time.Since(start), output, ctx.Err())
			}
			gotExit := 0
			if runErr != nil {
				exitErr, ok := runErr.(*exec.ExitError)
				if !ok {
					t.Fatalf("deploy execution failed: %v: %s", runErr, output)
				}
				gotExit = exitErr.ExitCode()
			}
			if gotExit != tc.wantExit || !strings.Contains(string(output), tc.wantOutput) {
				t.Fatalf("deploy exit=%d, output=%q; want exit=%d and %q", gotExit, output, tc.wantExit, tc.wantOutput)
			}
			args, err := os.ReadFile(argsFile)
			if err != nil {
				t.Fatal("curl did not run:", err)
			}
			if !strings.Contains(string(args), "--connect-timeout 2 --max-time 5") {
				t.Fatalf("curl must set both timeouts; got %q", args)
			}
		})
	}
}
