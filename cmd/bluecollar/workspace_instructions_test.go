package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

func TestLoadWorkspaceInstructionsFindsAgentsHierarchyAndSkills(t *testing.T) {
	repoRoot := t.TempDir()
	if errorValue := os.Mkdir(filepath.Join(repoRoot, ".git"), 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(repoRoot, "AGENTS.md"), []byte("root convention"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	subDirectory := filepath.Join(repoRoot, "pkg", "worker")
	if errorValue := os.MkdirAll(subDirectory, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filepath.Join(subDirectory, "AGENTS.md"), []byte("child convention"), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
	skillDirectory := filepath.Join(subDirectory, ".agents", "skills", "deploy")
	if errorValue := os.MkdirAll(skillDirectory, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	skillContent := "---\nname: deploy\ndescription: deploy safely\n---\nAlways run tests before deploy."
	if errorValue := os.WriteFile(filepath.Join(skillDirectory, "SKILL.md"), []byte(skillContent), 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}

	loaded := loadWorkspaceInstructions(subDirectory)

	rootIndex := strings.Index(loaded.prompt, "root convention")
	childIndex := strings.Index(loaded.prompt, "child convention")
	if rootIndex < 0 || childIndex < 0 || rootIndex > childIndex {
		t.Fatalf("expected root instructions before child instructions, got:\n%s", loaded.prompt)
	}
	foundDeploy := false
	for _, skill := range loaded.skills {
		if skill.Name == "deploy" && skill.Description == "deploy safely" {
			foundDeploy = true
		}
	}
	if !foundDeploy {
		t.Fatalf("expected workspace skill 'deploy' to load, got %+v", loaded.skills)
	}
}

func TestReadPipedPromptTrimsWhitespace(t *testing.T) {
	prompt := readPipedPrompt(strings.NewReader("  summarize the diff\n"))
	if prompt != "summarize the diff" {
		t.Fatalf("expected trimmed piped prompt, got %q", prompt)
	}
}

func TestReplCommandsManageHistoryModelAndStatus(t *testing.T) {
	session := &conversationSession{
		options:       runOptions{modelName: "model-a", endpointURL: "http://127.0.0.1:11434/v1"},
		workspacePath: "/tmp/work",
		history:       []agentcontract.VisibleContextMessage{{Speaker: "user", Text: "hello"}},
		turnCount:     2,
	}
	output := &bytes.Buffer{}

	handled, shouldExit := handleReplCommand(context.Background(), session, "/clear", output)
	if !handled || shouldExit || len(session.history) != 0 {
		t.Fatalf("expected /clear to reset history, handled=%t exit=%t len=%d", handled, shouldExit, len(session.history))
	}

	output.Reset()
	handled, shouldExit = handleReplCommand(context.Background(), session, "/model model-b", output)
	if !handled || shouldExit || session.options.modelName != "model-b" {
		t.Fatalf("expected /model to switch model, handled=%t exit=%t model=%q", handled, shouldExit, session.options.modelName)
	}

	output.Reset()
	handled, shouldExit = handleReplCommand(context.Background(), session, "/status", output)
	if !handled || shouldExit || !strings.Contains(output.String(), "model: model-b") || !strings.Contains(output.String(), "turns: 2") {
		t.Fatalf("expected /status to report model and turns, got %q", output.String())
	}

	handled, shouldExit = handleReplCommand(context.Background(), session, "/exit", output)
	if !handled || !shouldExit {
		t.Fatalf("expected /exit to terminate repl, handled=%t exit=%t", handled, shouldExit)
	}
}
