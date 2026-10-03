package evaltest

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func RequireInput(t *testing.T, name string, hint string) string {
	t.Helper()
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		t.Fatal(missingInputMessage(name, hint))
	}
	return value
}

func RequireExactly(t *testing.T, name string, expected string, hint string) {
	t.Helper()
	if os.Getenv(name) != expected {
		t.Fatal(missingInputMessage(name+"="+expected, hint))
	}
}

func missingInputMessage(name string, hint string) string {
	return fmt.Sprintf("the evaluation was requested with -tags llmeval, but %s is not set; %s", name, hint)
}

func RequireConfigured(t *testing.T, subject string, configurationError error) {
	t.Helper()
	if configurationError != nil {
		t.Fatalf("the evaluation was requested with -tags llmeval, but %s is not configured: %v", subject, configurationError)
	}
}
