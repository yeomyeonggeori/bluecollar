package agentcontract

import (
	"strings"
	"testing"
)

// Two message results are one requirement written twice: the fold keeps every
// description and hint for the judge, but the model sees one reply to give.
func TestMessageResultsFoldIntoOneReply(t *testing.T) {
	results := NormalizeExpectedResults([]ExpectedResult{
		{ID: "content_suggestion", Type: "message", Description: "게시글 보충 아이디어를 제안한다", Required: true, AcceptanceHints: []string{"보충 목록"}},
		{ID: "report", Type: "file", Description: "보고서 파일", Required: true},
		{ID: "final-message", Type: "message", Description: "A final reply explaining the outcome", Required: true, AcceptanceHints: []string{"결과 안내"}},
	})

	if len(results) != 2 {
		t.Fatalf("expected the message results folded into one, got %+v", results)
	}
	reply := results[0]
	if reply.Type != ExpectedResultTypeMessage || !reply.Required {
		t.Fatalf("expected one required message reply first, got %+v", reply)
	}
	for _, expected := range []string{"게시글 보충 아이디어를 제안한다", "A final reply explaining the outcome"} {
		if !strings.Contains(reply.Description, expected) {
			t.Fatalf("expected the folded description to keep %q, got %q", expected, reply.Description)
		}
	}
	if len(reply.AcceptanceHints) != 2 {
		t.Fatalf("expected hints united, got %+v", reply.AcceptanceHints)
	}
	if results[1].Type != ExpectedResultTypeFile {
		t.Fatalf("expected the file result untouched, got %+v", results[1])
	}
}

func TestASingleMessageResultIsLeftAlone(t *testing.T) {
	results := NormalizeExpectedResults([]ExpectedResult{
		{ID: "final-message", Type: "message", Description: "A final reply", Required: true},
	})
	if len(results) != 1 || results[0].Description != "A final reply" {
		t.Fatalf("expected the lone message result unchanged, got %+v", results)
	}
}
