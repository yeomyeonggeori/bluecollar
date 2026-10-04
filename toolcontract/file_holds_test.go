package toolcontract

import (
	"strings"
	"testing"
)

func TestFileHoldsKeepWhatFitsAndRefuseWhatDoesNot(t *testing.T) {
	holds, isKept := FileHoldsOf(map[string]any{"views": []string{"By quarter"}})
	if !isKept || string(holds) != `{"views":["By quarter"]}` {
		t.Fatalf("expected the holds kept as written, got %s %v", holds, isKept)
	}
	if _, isKept := FileHoldsOf(map[string]any{"views": []string{strings.Repeat("x", FileHoldsMaximumBytes)}}); isKept {
		t.Fatal("expected holds larger than the limit refused whole rather than cut")
	}
	if _, isKept := FileHoldsOf(map[string]any{}); isKept {
		t.Fatal("expected empty holds refused")
	}
}
