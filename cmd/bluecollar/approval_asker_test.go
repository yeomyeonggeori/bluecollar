package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/approval"
	"github.com/yeomyeonggeori/bluecollar/holdrecord"
)

func askWith(typed string, typingError error) (approval.Answer, string, string) {
	output := &bytes.Buffer{}
	shownPrompt := ""
	asker := terminalAsker{output: output, readLine: func(prompt string) (string, error) {
		shownPrompt = prompt
		return typed, typingError
	}}
	answer := asker.Ask(context.Background(), holdrecord.Hold{Call: agentcontract.HeldCall{Confirmation: "일정을 지울까요?"}})
	return answer, output.String(), shownPrompt
}

func TestTheTerminalShowsTheQuestionAndOffersAllowAndReject(t *testing.T) {
	_, shown, prompt := askWith("1", nil)

	if shown != "일정을 지울까요?\n" || prompt != "1) Allow  2) Reject ❯ " {
		t.Fatalf("the person sees the question and both choices, got %q %q", shown, prompt)
	}
}

func TestTypingTheAllowChoiceApproves(t *testing.T) {
	if answer, _, _ := askWith("1", nil); answer != approval.Approved {
		t.Fatalf("got %q", answer)
	}
}

func TestTypingTheRejectChoiceRejects(t *testing.T) {
	if answer, _, _ := askWith("2", nil); answer != approval.Rejected {
		t.Fatalf("got %q", answer)
	}
}

func TestAnythingElseTypedIsNoAnswer(t *testing.T) {
	for _, typed := range []string{"", "yes", "3", "1 2"} {
		if answer, _, _ := askWith(typed, nil); answer != approval.NoAnswer {
			t.Fatalf("%q decided %q; only a listed choice answers", typed, answer)
		}
	}
}

func TestAnInterruptedPromptIsNoAnswer(t *testing.T) {
	if answer, _, _ := askWith("1", errors.New("interrupted")); answer != approval.NoAnswer {
		t.Fatalf("a prompt that failed decides nothing, got %q", answer)
	}
}
