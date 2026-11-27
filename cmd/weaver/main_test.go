package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestHelpExitZero(t *testing.T) {
	for _, flagArg := range []string{"-h", "--help"} {
		var stdout, stderr bytes.Buffer
		code := runMainWith([]string{flagArg}, cliDeps{
			getwd:  func() (string, error) { return t.TempDir(), nil },
			stdin:  strings.NewReader(""),
			stdout: &stdout,
			stderr: &stderr,
		})
		if code != 0 {
			t.Fatalf("%s exit code = %d, want 0 (stderr=%q)", flagArg, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "Uso: weaver") {
			t.Fatalf("%s stderr missing usage:\n%s", flagArg, stderr.String())
		}
	}
}

func TestVersionCommandAndFlags(t *testing.T) {
	origV, origC, origD := version, commit, date
	version, commit, date = "1.2.3", "abc1234", "2026-11-27"
	t.Cleanup(func() {
		version, commit, date = origV, origC, origD
	})

	for _, args := range [][]string{{"version"}, {"-v"}, {"--version"}} {
		var stdout, stderr bytes.Buffer
		code := runMainWith(args, cliDeps{
			getwd:  func() (string, error) { return t.TempDir(), nil },
			stdin:  strings.NewReader(""),
			stdout: &stdout,
			stderr: &stderr,
		})
		if code != 0 {
			t.Fatalf("%v exit code = %d, want 0 (stderr=%q)", args, code, stderr.String())
		}
		got := stdout.String()
		if !strings.Contains(got, "1.2.3") || !strings.Contains(got, "abc1234") || !strings.Contains(got, "2026-11-27") {
			t.Fatalf("%v stdout = %q, want version/commit/date", args, got)
		}
	}
}

func TestUnknownSubcommandExitTwo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runMainWith([]string{"inventado"}, cliDeps{
		getwd:  func() (string, error) { return t.TempDir(), nil },
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: &stderr,
	})
	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "Uso: weaver") {
		t.Fatalf("stderr missing usage:\n%s", stderr.String())
	}
}

func TestUnexpectedExtraArgsExitTwo(t *testing.T) {
	for _, args := range [][]string{{"runs", "extra"}, {"doctor", "extra"}, {"version", "extra"}, {"config"}, {"config", "inventado"}, {"config", "show", "extra"}} {
		var stdout, stderr bytes.Buffer
		code := runMainWith(args, cliDeps{
			getwd:  func() (string, error) { return t.TempDir(), nil },
			stdin:  strings.NewReader(""),
			stdout: &stdout,
			stderr: &stderr,
		})
		if code != 2 {
			t.Fatalf("%v exit code = %d, want 2 (stderr=%q)", args, code, stderr.String())
		}
	}
}

func TestRunsOnNonGitExitOne(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := runMainWith([]string{"runs"}, cliDeps{
		getwd:  func() (string, error) { return dir, nil },
		stdin:  strings.NewReader(""),
		stdout: &stdout,
		stderr: &stderr,
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if stderr.Len() == 0 {
		t.Fatal("expected error message on stderr for non-git dir")
	}
}

func TestLogsBadArgCountExitTwo(t *testing.T) {
	for _, args := range [][]string{{"logs"}, {"logs", "a", "b", "c"}} {
		var stdout, stderr bytes.Buffer
		code := runMainWith(args, cliDeps{
			getwd:  func() (string, error) { return t.TempDir(), nil },
			stdin:  strings.NewReader(""),
			stdout: &stdout,
			stderr: &stderr,
		})
		if code != 2 {
			t.Fatalf("%v exit code = %d, want 2 (stderr=%q)", args, code, stderr.String())
		}
	}
}
