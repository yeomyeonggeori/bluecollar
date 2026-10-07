package main

import (
	"context"
	"fmt"
	"io"

	"github.com/yeomyeonggeori/blueprotocol/approvalcore"
	"github.com/yeomyeonggeori/blueprotocol/holdrecord"
)

const (
	approveChoice = "1"
	rejectChoice  = "2"
)

type terminalAsker struct {
	output   io.Writer
	readLine func(prompt string) (string, error)
}

func (asker terminalAsker) Ask(_ context.Context, hold holdrecord.Hold) approvalcore.Verdict {
	fmt.Fprintln(asker.output, hold.Call.Confirmation)
	choice, errorValue := asker.readLine("1) Allow  2) Reject ❯ ")
	if errorValue != nil {
		return approvalcore.Unanswered
	}
	switch choice {
	case approveChoice:
		return approvalcore.Approved
	case rejectChoice:
		return approvalcore.Rejected
	}
	return approvalcore.Unanswered
}
