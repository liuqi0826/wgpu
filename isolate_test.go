//go:build windows && amd64

package wgpu

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"
)

// isolatedCases are test bodies that would make wgpu-native abort the process
// if a Go-side check were missing. runIsolated runs them in a child process.
var isolatedCases = map[string]func(t *testing.T){}

const (
	isolatedEnv = "WGPU_ISOLATED_CASE"
	isolatedOK  = "ISOLATED-CASE-PASSED"
)

// runIsolated runs isolatedCases[name] in a child test process, so a missing
// check fails the test with the child's output instead of killing go test.
func runIsolated(t *testing.T, name string) {
	t.Helper()
	if _, ok := isolatedCases[name]; !ok {
		t.Fatalf("unknown isolated case %q", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestIsolatedChild$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), isolatedEnv+"="+name)
	out, err := cmd.CombinedOutput()
	var keep []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(strings.TrimSpace(line), "--- ") || strings.Contains(line, "_test.go:") {
			keep = append(keep, line)
		}
	}
	if err != nil || !bytes.Contains(out, []byte(isolatedOK)) {
		lines := strings.Split(string(out), "\n")
		if len(lines) > 60 {
			lines = lines[:60]
		}
		t.Fatalf("isolated case %q failed (%v):\n%s", name, err, strings.Join(lines, "\n"))
	}
	t.Log("child process output:\n" + strings.Join(keep, "\n"))
}

// TestIsolatedChild only runs as the child process of runIsolated.
func TestIsolatedChild(t *testing.T) {
	name := os.Getenv(isolatedEnv)
	if name == "" {
		t.Skip("only runs as a child process of runIsolated")
	}
	isolatedCases[name](t)
	if !t.Failed() {
		fmt.Println(isolatedOK)
	}
}

// registerIsolated adds cases under prefix and returns their sorted names.
func registerIsolated(prefix string, cases map[string]func(t *testing.T)) []string {
	names := make([]string, 0, len(cases))
	for name, fn := range cases {
		isolatedCases[prefix+name] = fn
		names = append(names, prefix+name)
	}
	slices.Sort(names)
	return names
}

// wantErr fails unless err is non-nil and contains want.
func wantErr(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("error %v, want %q", err, want)
	}
	t.Logf("%.200s", err)
}
