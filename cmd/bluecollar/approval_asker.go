package main

import (
	"context"
	"fmt"
	"io"

	"github.com/yeomyeonggeori/bluecollar/approval"
)

const (
	allowChoice  = "1"
	rejectChoice = "2"
)

type terminalAsker struct {
	output   io.Writer
	readLine func(prompt string) (string, error)
}

func (asker terminalAsker) Ask(_ context.Context, hold approval.Hold) approval.Answer {
	fmt.Fprintln(asker.output, hold.Call.Confirmation)
	choice, errorValue := asker.readLine("1) Allow  2) Reject ❯ ")
	if errorValue != nil {
		return approval.NoAnswer
	}
	switch choice {
	case allowChoice:
		return approval.Approved
	case rejectChoice:
		return approval.Rejected
	}
	return approval.NoAnswer
}
