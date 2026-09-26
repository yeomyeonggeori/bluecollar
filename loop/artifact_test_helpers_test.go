package loop

import (
	"testing"
)

func writeValidHTMLTestFile(t *testing.T, path string) {
	t.Helper()
	writeAgentTestFile(t, path, "<html><body><main>Deck</main></body></html>")
}
