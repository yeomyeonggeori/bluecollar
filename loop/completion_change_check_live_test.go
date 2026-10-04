//go:build llmeval

package loop

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
)

type deliveredFileLiveCase struct {
	name          string
	prompt        string
	conversation  []string
	command       string
	filename      string
	contentType   string
	isUnfulfilled bool
}

var deliveredFileLiveCases = []deliveredFileLiveCase{
	{
		name:        "one-page pdf",
		prompt:      "Make a one-page PDF whose only line reads 'native install rig 7d9a1a5a', and send me the PDF file itself. 7d9a1a5a",
		command:     `cd ~/documents && python3 -c "from reportlab.pdfgen import canvas; c = canvas.Canvas('native-install-rig.pdf'); c.drawString(72, 720, 'native install rig 7d9a1a5a'); c.save()"`,
		filename:    "native-install-rig.pdf",
		contentType: "application/pdf",
	},
	{
		name:        "xlsx in korean",
		prompt:      "이번 달 비품 구매 목록을 엑셀 파일로 만들어서 보내줘. 품목은 모니터 2대, 키보드 3개야",
		command:     `cd ~/documents && python3 -c "import openpyxl; w = openpyxl.Workbook(); s = w.active; s.append(['품목', '수량']); s.append(['모니터', 2]); s.append(['키보드', 3]); w.save('비품-구매-목록.xlsx')"`,
		filename:    "비품-구매-목록.xlsx",
		contentType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	},
	{
		name:        "docx",
		prompt:      "Draft a short meeting-notes docx for today's design review and attach it.",
		command:     `cd ~/documents && python3 -c "import docx; d = docx.Document(); d.add_heading('Design review', 1); d.add_paragraph('Notes'); d.save('design-review-notes.docx')"`,
		filename:    "design-review-notes.docx",
		contentType: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	},
	{
		name:         "send the file again",
		prompt:       "아까 만든 보고서 파일 다시 보내줘",
		conversation: []string{"이샘플: 3분기 매출 보고서 PDF로 만들어줘", "김인턴: 3분기 매출 보고서를 PDF로 만들어 첨부했습니다."},
		command:      `ls ~/documents`,
		filename:     "3분기-매출-보고서.pdf",
		contentType:  "application/pdf",
	},
	{
		name:          "an old file instead of the asked one",
		prompt:        "Make a one-page PDF whose only line reads 'native install rig 7d9a1a5a', and send me the PDF file itself. 7d9a1a5a",
		command:       `ls ~/documents`,
		filename:      "2025-invoice.pdf",
		contentType:   "application/pdf",
		isUnfulfilled: true,
	},
	{
		name:          "a pdf with other words",
		prompt:        "Make a one-page PDF whose only line reads 'native install rig 7d9a1a5a', and send me the PDF file itself. 7d9a1a5a",
		command:       `cd ~/documents && python3 -c "from reportlab.pdfgen import canvas; c = canvas.Canvas('native-install-rig.pdf'); c.drawString(72, 720, 'Lorem ipsum placeholder'); c.save()"`,
		filename:      "native-install-rig.pdf",
		contentType:   "application/pdf",
		isUnfulfilled: true,
	},
}

func TestLiveDeliveredFileSatisfiesTheChangeCheck(t *testing.T) {
	apiKey := os.Getenv("OPENROUTER_API_KEY")
	if apiKey == "" {
		t.Skip("set OPENROUTER_API_KEY")
	}
	languageModel, errorValue := (openaicompatible.Endpoint{URL: "https://openrouter.ai/api/v1", ModelName: firstNonEmptyString(os.Getenv("BLUECOLLAR_MODEL_NAME"), "z-ai/glm-5.3-flash"), APIKey: apiKey}).Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	decisionModel := decisions.Endpoint{URL: decisions.DefaultEndpointURL, ModelName: firstNonEmptyString(os.Getenv("BLUECOLLAR_DECISION_MODEL"), "~typesafe/jev-latest"), APIKey: apiKey}.DecisionModel()
	services := newTurnRunnerTestServices(languageModel, TurnOptions{})
	toolSet := kernelFileToolSet()
	wrongVerdicts := 0
	attempts := 0
	for _, liveCase := range deliveredFileLiveCases {
		request := AgentTurnRequest{Prompt: liveCase.prompt, ToolSet: toolSet}
		request.VisibleContext.Messages = visibleMessages(liveCase.conversation)
		for attempt := 0; attempt < 3; attempt++ {
			attempts++
			expected, _ := services.runner.defineExpectedChanges(context.Background(), "live", request)
			check, errorValue := checkExpectedChanges(context.Background(), decisionModel, request, expected, fileMadeByCommandThenDelivered(liveCase.command, liveCase.filename, liveCase.contentType), claimEvidence{})
			if errorValue != nil {
				t.Fatal(errorValue)
			}
			isRefused := len(check.Unmet) > 0
			verdict := "ok"
			if isRefused != liveCase.isUnfulfilled {
				verdict = "WRONG"
				wrongVerdicts++
			}
			document, _ := json.Marshal(check)
			t.Logf("%s refused=%v [%s] #%d: %s", verdict, isRefused, liveCase.name, attempt, document)
		}
	}
	t.Logf("wrong: %d of %d", wrongVerdicts, attempts)
}

func visibleMessages(lines []string) []VisibleContextMessage {
	messages := []VisibleContextMessage{}
	for _, line := range lines {
		speaker, text, _ := strings.Cut(line, ": ")
		messages = append(messages, VisibleContextMessage{Speaker: speaker, Text: text})
	}
	return messages
}
