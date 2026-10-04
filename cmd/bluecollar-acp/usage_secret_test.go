package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestUsageNeverPrintsTheAPIKeyFromTheEnvironment(t *testing.T) {
	binaryPath := filepath.Join(t.TempDir(), "agent")
	if output, errorValue := exec.Command("go", "build", "-o", binaryPath, ".").CombinedOutput(); errorValue != nil {
		t.Fatalf("build failed: %v\n%s", errorValue, output)
	}
	command := exec.Command(binaryPath, "-no-such-flag")
	command.Env = append(os.Environ(), "BLUECOLLAR_LLM_API_KEY=usage-secret-marker")
	output, _ := command.CombinedOutput()
	if !strings.Contains(string(output), "-api-key") {
		t.Fatalf("expected the usage text, got %s", output)
	}
	if strings.Contains(string(output), "usage-secret-marker") {
		t.Fatalf("usage printed the API key from the environment: %s", output)
	}
}
