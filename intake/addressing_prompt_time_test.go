package intake

import (
	"strings"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

func TestAddressingPromptCarriesMessageAndContextTime(t *testing.T) {
	sentAt := time.Date(2026, 7, 10, 14, 3, 0, 0, agentcontract.CompanyLocation("Asia/Seoul"))
	prompt := addressingClassificationPrompt(agentcontract.AddressingClassificationRequest{
		Prompt:        "and this too",
		MessageSentAt: sentAt.Add(30 * time.Second),
		VisibleContext: agentcontract.VisibleContext{
			Messages: []agentcontract.VisibleContextMessage{
				{Speaker: "Wendy", SpeakerCallingName: "Wendy", Text: "tidy it up", SentAt: sentAt},
			},
		},
		Company: agentcontract.CompanyContext{TimeZone: "Asia/Seoul"},
	})
	if !strings.Contains(prompt, "messageTime: 2026-07-10 14:03") {
		t.Fatalf("expected messageTime line, got %q", prompt)
	}
	if !strings.Contains(prompt, "context: [2026-07-10 14:03]") {
		t.Fatalf("expected timestamped context line, got %q", prompt)
	}
}
