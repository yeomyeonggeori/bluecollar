package loop

import (
	"strings"
	"testing"
)

func TestTheEndOfAnOutputSurvivesTruncation(t *testing.T) {
	content := "start of a long build log" + strings.Repeat("x", 500) + "error: undefined reference to main"

	elided := withMiddleElided(content, 100)

	if !strings.Contains(elided, "error: undefined reference to main") {
		t.Fatalf("a command's verdict is at its end and cutting it away hides why the command failed, got %q", elided)
	}
	if !strings.Contains(elided, "start of a long build log") {
		t.Fatalf("what the command was doing is at its start, got %q", elided)
	}
}

func TestTruncationSaysHowMuchItDropped(t *testing.T) {
	content := strings.Repeat("x", 1000)

	elided := withMiddleElided(content, 100)

	if !strings.Contains(elided, "900 characters elided") {
		t.Fatalf("an agent deciding whether to look again needs to know how much it has not seen, got %q", elided)
	}
}

func TestShortOutputIsLeftAlone(t *testing.T) {
	content := "exit code 0"

	if withMiddleElided(content, 100) != content {
		t.Fatal("output that fits must arrive exactly as the command wrote it")
	}
}

func TestACutNeverLandsInsideACharacter(t *testing.T) {
	content := strings.Repeat("한", 400)

	elided := withMiddleElided(content, 101)

	if !strings.HasPrefix(content, elided[:1]) {
		t.Fatal("a cut through a multi-byte character leaves bytes no reader can decode")
	}
	for _, character := range elided {
		if character == '�' {
			t.Fatalf("truncation produced a replacement character, so it cut mid-rune: %q", elided)
		}
	}
}
