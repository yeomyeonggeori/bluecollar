package bluecollar_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

const nameOfOneHost = "blue" + "claw"

func TestNoTrackedFileNamesAnyOneHost(t *testing.T) {
	listing, errorValue := exec.Command("git", "ls-files", "-z").Output()
	if errorValue != nil {
		t.Skipf("the tracked file list needs a git checkout: %v", errorValue)
	}
	for _, path := range strings.Split(strings.TrimRight(string(listing), "\x00"), "\x00") {
		if path == "go.sum" {
			continue
		}
		if mentionsHost(t, path) {
			t.Errorf("%s names %q; this repository is a harness that serves every host", path, nameOfOneHost)
		}
	}
}

func mentionsHost(t *testing.T, path string) bool {
	t.Helper()
	if strings.Contains(strings.ToLower(path), nameOfOneHost) {
		return true
	}
	content, errorValue := os.ReadFile(path)
	if errorValue != nil {
		return false
	}
	return bytes.Contains(bytes.ToLower(content), []byte(nameOfOneHost))
}
