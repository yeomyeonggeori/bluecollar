package main

import (
	"fmt"
	"os"
	"strings"
)

const mascotColumns = 16
const welcomeInnerWidth = 44
const welcomeLabelWidth = 11

const mascotBlue = 68
const mascotSkin = 173
const mascotWhite = 255
const mascotBoot = 236

type mascotSpan struct {
	start     int
	end       int
	colorCode int
}

var mascotRows = [][]mascotSpan{
	{{4, 11, mascotBlue}},
	{{4, 14, mascotBlue}},
	{{4, 11, mascotSkin}},
	{{4, 5, mascotSkin}, {7, 8, mascotSkin}, {10, 11, mascotSkin}},
	{{4, 11, mascotSkin}},
	{{4, 11, mascotSkin}},
	{{1, 2, mascotSkin}, {3, 4, mascotWhite}, {5, 5, mascotBlue}, {6, 9, mascotWhite}, {10, 10, mascotBlue}, {11, 12, mascotWhite}, {13, 14, mascotSkin}},
	{{4, 11, mascotBlue}},
	{{4, 11, mascotBlue}},
	{{5, 6, mascotBlue}, {9, 10, mascotBlue}},
	{{4, 6, mascotBoot}, {9, 11, mascotBoot}},
}

func mascotCells(spans []mascotSpan) []int {
	cells := make([]int, mascotColumns)
	for _, span := range spans {
		for column := span.start; column <= span.end; column++ {
			cells[column] = span.colorCode
		}
	}
	return cells
}

func mascotHalfBlockCell(upperColor int, lowerColor int) string {
	switch {
	case upperColor == 0 && lowerColor == 0:
		return " "
	case lowerColor == 0:
		return fmt.Sprintf("\x1b[38;5;%dm▀%s", upperColor, styleReset)
	case upperColor == 0:
		return fmt.Sprintf("\x1b[38;5;%dm▄%s", lowerColor, styleReset)
	case upperColor == lowerColor:
		return fmt.Sprintf("\x1b[38;5;%dm█%s", upperColor, styleReset)
	}
	return fmt.Sprintf("\x1b[38;5;%d;48;5;%dm▀%s", upperColor, lowerColor, styleReset)
}

func mascotLines() []string {
	rows := mascotRows
	if len(rows)%2 == 1 {
		rows = append(rows, nil)
	}
	lines := make([]string, 0, len(rows)/2)
	for pair := 0; pair < len(rows); pair += 2 {
		upper, lower := mascotCells(rows[pair]), mascotCells(rows[pair+1])
		line := ""
		for column := 0; column < mascotColumns; column++ {
			line += mascotHalfBlockCell(upper[column], lower[column])
		}
		lines = append(lines, line)
	}
	return lines
}

func visibleWidth(line string) int {
	width, isInEscape := 0, false
	for _, character := range line {
		if isInEscape {
			isInEscape = character != 'm'
			continue
		}
		if character == '\x1b' {
			isInEscape = true
			continue
		}
		width++
	}
	return width
}

func welcomeInk() string {
	return fmt.Sprintf("\x1b[38;5;%dm", mascotBlue)
}

func welcomeBoxLine(content string, isCentered bool) string {
	padding := welcomeInnerWidth - visibleWidth(content)
	left, right := 1, padding-1
	if isCentered {
		left = padding / 2
		right = padding - left
	}
	return welcomeInk() + "│" + styleReset + strings.Repeat(" ", left) + content + strings.Repeat(" ", right) + welcomeInk() + "│" + styleReset
}

func welcomeDetailLine(label string, value string) string {
	return welcomeBoxLine(styleDim+fmt.Sprintf("%-*s", welcomeLabelWidth, label)+styleReset+value, false)
}

func printWelcome(modelName string, workspacePath string) {
	title := " bluecollar "
	lines := []string{
		welcomeInk() + "╭─" + styleBold + title + styleReset + welcomeInk() + strings.Repeat("─", welcomeInnerWidth-len(title)-1) + "╮" + styleReset,
		welcomeBoxLine("", false),
	}
	for _, line := range mascotLines() {
		lines = append(lines, welcomeBoxLine(line, true))
	}
	lines = append(lines,
		welcomeBoxLine("", false),
		welcomeBoxLine(styleBold+"Welcome to the shift."+styleReset, true),
		welcomeBoxLine("", false),
		welcomeDetailLine("model", modelName),
		welcomeDetailLine("workspace", abbreviatedHomePath(workspacePath)),
		welcomeDetailLine("leave", "/exit"),
		welcomeBoxLine("", false),
		welcomeInk()+"╰"+strings.Repeat("─", welcomeInnerWidth)+"╯"+styleReset,
	)
	fmt.Fprintln(os.Stderr, strings.Join(lines, "\n"))
}

func abbreviatedHomePath(path string) string {
	home, homeError := os.UserHomeDir()
	if homeError != nil || home == "" || !strings.HasPrefix(path, home) {
		return path
	}
	return "~" + strings.TrimPrefix(path, home)
}
