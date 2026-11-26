package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

var fakeCLIBin string

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "weaver-fakecli-*")
	if err != nil {
		fmt.Fprintf(os.Stderr, "provider TestMain: mkdir: %v\n", err)
		os.Exit(1)
	}
	defer os.RemoveAll(tmpDir)

	binName := "fakecli"
	if runtime.GOOS == "windows" {
		binName += ".exe"
	}
	fakeCLIBin = filepath.Join(tmpDir, binName)

	cmd := exec.Command("go", "build", "-o", fakeCLIBin, "./testdata/fakecli")
	if out, err := cmd.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "provider TestMain: build fakecli: %v\n%s\n", err, string(out))
		os.Exit(1)
	}

	os.Exit(m.Run())
}
