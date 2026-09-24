package main

import (
	"context"
	"errors"

	"github.com/yeomyeonggeori/bluecollar/loop"
)

type shellSpillStore struct {
	runningShell shell
}

func (store shellSpillStore) SaveToolResultSpill(ctx context.Context, spill loop.ToolResultSpill) (loop.ToolResultSpillRef, error) {
	path := store.runningShell.spilledOutputPath(ctx, spill.Content)
	if path == "" {
		return loop.ToolResultSpillRef{}, errors.New("the shell could not create or write a temporary file for the spill")
	}
	return loop.ToolResultSpillRef{
		Locator:       path,
		Bytes:         len(spill.Content),
		RetrievalHint: "Read it with file_read, or pull out just the part you need with grep or sed through bash.",
	}, nil
}
