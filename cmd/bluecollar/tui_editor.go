package main

import (
	"strings"
	"unicode"
)

type slashCommandSpec struct {
	name        string
	usage       string
	description string
}

var tuiSlashCommands = []slashCommandSpec{
	{name: "/login", usage: "/login [codex]", description: "sign in with ChatGPT subscription"},
	{name: "/model", usage: "/model [name]", description: "show or switch model"},
	{name: "/models", usage: "/models", description: "list models on endpoint"},
	{name: "/endpoint", usage: "/endpoint [url|codex|gemini-api]", description: "show or switch provider"},
	{name: "/apikey", usage: "/apikey <key>", description: "set endpoint API key"},
	{name: "/ledger", usage: "/ledger [full|compact]", description: "toggle ledger verbosity"},
	{name: "/clear", usage: "/clear", description: "clear conversation and screen"},
	{name: "/status", usage: "/status", description: "show model, turns and tokens"},
	{name: "/trace", usage: "/trace <path>", description: "write last turn trace"},
	{name: "/queue", usage: "/queue <prompt>", description: "queue follow-up after current turn"},
	{name: "/help", usage: "/help", description: "list commands and shortcuts"},
	{name: "/exit", usage: "/exit", description: "leave the shift"},
}

type composerEditor struct {
	runes        []rune
	cursor       int
	history      []string
	historyIndex int
	draftBefore  string
}

func newComposerEditor() *composerEditor {
	return &composerEditor{historyIndex: -1}
}

func (editor *composerEditor) text() string {
	return string(editor.runes)
}

func (editor *composerEditor) setText(value string) {
	editor.runes = []rune(value)
	editor.cursor = len(editor.runes)
}

func (editor *composerEditor) clear() {
	editor.runes = nil
	editor.cursor = 0
	editor.historyIndex = -1
	editor.draftBefore = ""
}

func (editor *composerEditor) recordHistory(entry string) {
	trimmed := strings.TrimSpace(entry)
	if trimmed == "" {
		return
	}
	if len(editor.history) > 0 && editor.history[len(editor.history)-1] == trimmed {
		editor.historyIndex = -1
		return
	}
	editor.history = append(editor.history, trimmed)
	editor.historyIndex = -1
	editor.draftBefore = ""
}

func (editor *composerEditor) insertRune(character rune) {
	editor.runes = append(editor.runes, 0)
	copy(editor.runes[editor.cursor+1:], editor.runes[editor.cursor:])
	editor.runes[editor.cursor] = character
	editor.cursor++
}

func (editor *composerEditor) insertText(text string) {
	inserted := []rune(text)
	if len(inserted) == 0 {
		return
	}
	updated := make([]rune, 0, len(editor.runes)+len(inserted))
	updated = append(updated, editor.runes[:editor.cursor]...)
	updated = append(updated, inserted...)
	updated = append(updated, editor.runes[editor.cursor:]...)
	editor.runes = updated
	editor.cursor += len(inserted)
}

func (editor *composerEditor) backspace() {
	if editor.cursor <= 0 {
		return
	}
	editor.runes = append(editor.runes[:editor.cursor-1], editor.runes[editor.cursor:]...)
	editor.cursor--
}

func (editor *composerEditor) deleteForward() {
	if editor.cursor >= len(editor.runes) {
		return
	}
	editor.runes = append(editor.runes[:editor.cursor], editor.runes[editor.cursor+1:]...)
}

func (editor *composerEditor) deleteWordBackward() {
	if editor.cursor <= 0 {
		return
	}
	target := editor.cursor
	for target > 0 && unicode.IsSpace(editor.runes[target-1]) {
		target--
	}
	for target > 0 && !unicode.IsSpace(editor.runes[target-1]) {
		target--
	}
	editor.runes = append(editor.runes[:target], editor.runes[editor.cursor:]...)
	editor.cursor = target
}

func (editor *composerEditor) killToLineEnd() {
	end := editor.lineEndIndex()
	if end == editor.cursor && end < len(editor.runes) {
		end++
	}
	editor.runes = append(editor.runes[:editor.cursor], editor.runes[end:]...)
}

func (editor *composerEditor) killToLineStart() {
	start := editor.lineStartIndex()
	editor.runes = append(editor.runes[:start], editor.runes[editor.cursor:]...)
	editor.cursor = start
}

func (editor *composerEditor) moveLeft() {
	if editor.cursor > 0 {
		editor.cursor--
	}
}

func (editor *composerEditor) moveRight() {
	if editor.cursor < len(editor.runes) {
		editor.cursor++
	}
}

func (editor *composerEditor) moveLineStart() {
	editor.cursor = editor.lineStartIndex()
}

func (editor *composerEditor) moveLineEnd() {
	editor.cursor = editor.lineEndIndex()
}

func (editor *composerEditor) lineStartIndex() int {
	index := editor.cursor
	for index > 0 && editor.runes[index-1] != '\n' {
		index--
	}
	return index
}

func (editor *composerEditor) lineEndIndex() int {
	index := editor.cursor
	for index < len(editor.runes) && editor.runes[index] != '\n' {
		index++
	}
	return index
}

func (editor *composerEditor) historyPrevious() bool {
	if len(editor.history) == 0 {
		return false
	}
	if editor.historyIndex == -1 {
		editor.draftBefore = editor.text()
		editor.historyIndex = len(editor.history) - 1
	} else if editor.historyIndex > 0 {
		editor.historyIndex--
	}
	editor.setText(editor.history[editor.historyIndex])
	return true
}

func (editor *composerEditor) historyNext() bool {
	if editor.historyIndex == -1 {
		return false
	}
	if editor.historyIndex < len(editor.history)-1 {
		editor.historyIndex++
		editor.setText(editor.history[editor.historyIndex])
		return true
	}
	editor.historyIndex = -1
	editor.setText(editor.draftBefore)
	return true
}

func (editor *composerEditor) matchingSlashCommands() []slashCommandSpec {
	text := editor.text()
	if !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") || strings.Contains(text, " ") {
		return nil
	}
	matches := []slashCommandSpec{}
	for _, command := range tuiSlashCommands {
		if strings.HasPrefix(command.name, text) {
			matches = append(matches, command)
		}
	}
	return matches
}

func (editor *composerEditor) completeSlashCommand() bool {
	matches := editor.matchingSlashCommands()
	if len(matches) == 0 {
		return false
	}
	completed := matches[0].name
	if strings.Contains(matches[0].usage, " ") {
		completed += " "
	}
	editor.setText(completed)
	return true
}

type composerVisualLayout struct {
	lines        []string
	cursorRow    int
	cursorColumn int
}

func (editor *composerEditor) layout(width int, maximumLines int) composerVisualLayout {
	if width < 8 {
		width = 8
	}
	if maximumLines < 1 {
		maximumLines = 1
	}
	lines := []string{""}
	row := 0
	column := 0
	cursorRow := 0
	cursorColumn := 0
	for index, character := range editor.runes {
		if index == editor.cursor {
			cursorRow = row
			cursorColumn = column
		}
		if character == '\n' {
			lines = append(lines, "")
			row++
			column = 0
			continue
		}
		charWidth := runeDisplayWidth(character)
		if column+charWidth > width && column > 0 {
			lines = append(lines, "")
			row++
			column = 0
		}
		lines[row] += string(character)
		column += charWidth
	}
	if editor.cursor == len(editor.runes) {
		cursorRow = row
		cursorColumn = column
	}
	startRow := 0
	if len(lines) > maximumLines {
		if cursorRow >= maximumLines {
			startRow = cursorRow - maximumLines + 1
		}
		endRow := min(startRow+maximumLines, len(lines))
		lines = lines[startRow:endRow]
		cursorRow -= startRow
	}
	return composerVisualLayout{
		lines:        lines,
		cursorRow:    cursorRow,
		cursorColumn: cursorColumn,
	}
}
