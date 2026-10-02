package agentcontract

import "strings"

const memorySummaryContentLimit = 240

// BuildMemoryContext renders what a turn already knows: one statement to a
// line, dated where the fact carries a date.
//
// A fact's score, its origin and the file it was found in are how it was
// retrieved and not what it says, so they stay out of a prompt that is
// charged on every turn. The date stays because a statement whose moment is
// unknown answers a question about when with a guess.
func BuildMemoryContext(memoryFacts []MemoryFact) string {
	statements := []string{}
	for _, memoryFact := range memoryFacts {
		statement := compactMemoryContent(memoryFact.Content)
		if statement == "" {
			continue
		}
		if !memoryFact.ValidAt.IsZero() {
			statement = "[" + memoryFact.ValidAt.Format("2006-01-02") + "] " + statement
		}
		statements = append(statements, "- "+statement)
	}
	if len(statements) == 0 {
		return ""
	}
	return "What you already know:\n" + strings.Join(statements, "\n")
}

func compactMemoryContent(content string) string {
	trimmedContent := strings.Join(strings.Fields(strings.TrimSpace(content)), " ")
	if trimmedContent == "" {
		return ""
	}
	runes := []rune(trimmedContent)
	if len(runes) <= memorySummaryContentLimit {
		return trimmedContent
	}
	return string(runes[:memorySummaryContentLimit]) + "..."
}
