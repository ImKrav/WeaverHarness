package procx

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	switch os.Getenv("PROCX_ROLE") {
	case "parent":
		cmd := exec.Command(os.Args[0])
		cmd.Env = append(os.Environ(), "PROCX_ROLE=child")
		if err := cmd.Run(); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	case "child":
		pidFile := os.Getenv("PROCX_PID_FILE")
		tmp := pidFile + ".tmp"
		if err := os.WriteFile(tmp, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			os.Exit(2)
		}
		if err := os.Rename(tmp, pidFile); err != nil {
			os.Exit(3)
		}
		time.Sleep(60 * time.Second)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestKillTree(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(),
		"PROCX_ROLE=parent",
		"PROCX_PID_FILE="+pidFile,
	)

	p, err := Start(cmd)
	if err != nil {
		t.Fatalf("Start() error: %v", err)
	}

	var childPID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(pidFile)
		if err == nil {
			n, convErr := strconv.Atoi(strings.TrimSpace(string(data)))
			if convErr == nil && n > 0 {
				childPID = n
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	if childPID == 0 {
		_ = p.Kill()
		_ = p.Wait()
		t.Fatal("timed out waiting for child process PID file")
	}

	if !alive(childPID) {
		_ = p.Kill()
		_ = p.Wait()
		t.Fatalf("child PID %d not alive before Kill()", childPID)
	}

	if err := p.Kill(); err != nil {
		t.Fatalf("first Kill() error: %v", err)
	}
	if err := p.Kill(); err != nil {
		t.Fatalf("second (idempotent) Kill() error: %v", err)
	}
	_ = p.Wait()

	killDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(killDeadline) {
		if !alive(childPID) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("child PID %d still alive 5s after Kill()", childPID)
}
