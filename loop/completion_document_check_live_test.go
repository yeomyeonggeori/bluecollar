//go:build llmeval

package loop

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type documentVariant struct {
	name       string
	lines      []string
	isFaithful bool
}

type documentLiveCase struct {
	name     string
	format   string
	language string
	prompt   string
	variants []documentVariant
}

var documentLiveCases = []documentLiveCase{
	{
		name: "lease", format: "docx", language: "en",
		prompt: "Draft a residential lease for the unit at 14 Alder Court with a monthly rent of USD 2,150, a 24-month term starting 2027-03-01, and a security deposit of USD 4,300. Send me the Word file.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"RESIDENTIAL LEASE AGREEMENT", "Premises: 14 Alder Court, Unit 3B", "1. Term. The lease runs for twenty-four (24) months beginning March 1, 2027.", "2. Rent. Tenant pays 2,150 US dollars per month, due on the first day of each month.", "3. Security Deposit. Tenant deposits USD 4,300 on signing.", "4. Pets. No pets without written consent.", "5. Governing law. This lease is governed by the laws of the state where the premises are located."}},
			{name: "term differs", lines: []string{"RESIDENTIAL LEASE AGREEMENT", "Premises: 14 Alder Court, Unit 3B", "1. Term. The lease runs for twelve (12) months beginning March 1, 2027.", "2. Rent. Tenant pays 2,150 US dollars per month, due on the first day of each month.", "3. Security Deposit. Tenant deposits USD 4,300 on signing.", "4. Pets. No pets without written consent."}},
		},
	},
	{
		name: "purchase order", format: "xlsx", language: "ko",
		prompt: "발주서 엑셀 만들어줘. 공급사는 한빛금속, 산업용 선반 40개, 단가 185,000원, 납기 2027년 1월 15일이야.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"발주서", "공급사 | 한빛금속", "품목 | 수량 | 단가 | 금액", "산업용 선반 | 40 | 185,000 | 7,400,000", "납기일 | 2027-01-15", "결제조건 | 납품 후 30일 이내 현금"}},
			{name: "quantity differs", lines: []string{"발주서", "공급사 | 한빛금속", "품목 | 수량 | 단가 | 금액", "산업용 선반 | 30 | 185,000 | 5,550,000", "납기일 | 2027-01-15", "결제조건 | 납품 후 30일 이내 현금"}},
		},
	},
	{
		name: "job offer", format: "pdf", language: "en",
		prompt: "Write an offer letter PDF for 최견본 for the Data Analyst role: base salary USD 96,000 a year, start date February 8, 2027, and 20 days of paid time off.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"Offer of Employment", "Dear 최견본,", "We are pleased to offer you the position of Data Analyst.", "Your annual base salary will be $96,000, paid semi-monthly.", "Your first day will be Monday, February 8, 2027.", "You will receive twenty (20) days of paid time off per year.", "This offer is contingent on a background check.", "Sincerely, People Operations, hr@example.com"}},
			{name: "salary differs", lines: []string{"Offer of Employment", "Dear 최견본,", "We are pleased to offer you the position of Data Analyst.", "Your annual base salary will be $69,000, paid semi-monthly.", "Your first day will be Monday, February 8, 2027.", "You will receive twenty (20) days of paid time off per year.", "Sincerely, People Operations, hr@example.com"}},
		},
	},
	{
		name: "service agreement", format: "docx", language: "ko",
		prompt: "서버 유지보수 용역계약서 워드로 작성해줘. 월 용역료 1,200,000원, 계약기간 2027년 1월 1일부터 12월 31일까지, 장애 발생 시 4시간 이내 대응 조항 꼭 넣어줘.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"서버 유지보수 용역계약서", "제1조 (목적) 본 계약은 갑의 서버 유지보수 업무를 을에게 위탁하는 데 필요한 사항을 정한다.", "제2조 (계약기간) 2027년 1월 1일부터 2027년 12월 31일까지로 한다.", "제3조 (용역료) 갑은 을에게 매월 금 1,200,000원(부가세 별도)을 지급한다.", "제4조 (장애 대응) 을은 장애 접수 후 4시간 이내에 대응을 개시한다.", "제5조 (비밀유지) 양 당사자는 업무상 알게 된 정보를 누설하지 않는다."}},
			{name: "clause missing", lines: []string{"서버 유지보수 용역계약서", "제1조 (목적) 본 계약은 갑의 서버 유지보수 업무를 을에게 위탁하는 데 필요한 사항을 정한다.", "제2조 (계약기간) 2027년 1월 1일부터 2027년 12월 31일까지로 한다.", "제3조 (용역료) 갑은 을에게 매월 금 1,200,000원(부가세 별도)을 지급한다.", "제4조 (비밀유지) 양 당사자는 업무상 알게 된 정보를 누설하지 않는다.", "제5조 (분쟁) 분쟁은 갑의 소재지 관할 법원에서 해결한다."}},
		},
	},
	{
		name: "meeting minutes", format: "docx", language: "en",
		prompt: "Turn my notes into meeting minutes as a docx: we moved the launch to June 9, capped the campaign budget at EUR 18,000, and 박예시 owns the vendor follow-up.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"Marketing Sync - Minutes", "Attendees: marketing team", "Decisions", "- Launch date moved to 9 June.", "- Campaign budget capped at €18,000.", "Action items", "- 박예시: follow up with the vendor.", "Next meeting: to be scheduled."}},
			{name: "date differs", lines: []string{"Marketing Sync - Minutes", "Attendees: marketing team", "Decisions", "- Launch date moved to 19 June.", "- Campaign budget capped at €18,000.", "Action items", "- 박예시: follow up with the vendor."}},
		},
	},
	{
		name: "price quote", format: "pdf", language: "ko",
		prompt: "노트북 견적서 PDF로 만들어줘. 12대, 대당 1,490,000원, 부가세 별도, 견적 유효기간 30일.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"견 적 서", "수신: 고객사 구매 담당자님", "품명 노트북 | 수량 12 | 단가 1,490,000 | 공급가액 17,880,000", "부가세 별도", "견적 유효기간: 견적일로부터 30일", "납품: 발주 후 2주 이내", "문의: sales@example.com"}},
			{name: "validity differs", lines: []string{"견 적 서", "수신: 고객사 구매 담당자님", "품명 노트북 | 수량 12 | 단가 1,490,000 | 공급가액 17,880,000", "부가세 별도", "견적 유효기간: 견적일로부터 15일", "문의: sales@example.com"}},
		},
	},
	{
		name: "event invitation", format: "pdf", language: "en",
		prompt: "Make a PDF invitation for our partner evening at Riverside Hall on April 22, 2027 at 6:30 pm. Ask guests to RSVP by April 15.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"You're invited", "Partner Evening", "Riverside Hall", "Thursday, 22 April 2027, 18:30", "Light dinner and live music", "Please RSVP by 15 April to events@example.com"}},
			{name: "venue differs", lines: []string{"You're invited", "Partner Evening", "Lakeview Pavilion", "Thursday, 22 April 2027, 18:30", "Light dinner and live music", "Please RSVP by 15 April to events@example.com"}},
		},
	},
	{
		name: "travel itinerary", format: "xlsx", language: "ko",
		prompt: "출장 일정표 엑셀로 정리해줘. 2027년 5월 3일 인천에서 프랑크푸르트로 LH713편 출발, 5월 9일 귀국, 호텔은 4박이야.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"출장 일정표", "날짜 | 일정 | 비고", "2027-05-03 | 인천 → 프랑크푸르트 LH713 | 출국", "2027-05-04 | 현지 미팅 |", "2027-05-05 ~ 2027-05-08 | 호텔 숙박 (4박) | 시내 호텔", "2027-05-09 | 귀국 |"}},
			{name: "return differs", lines: []string{"출장 일정표", "날짜 | 일정 | 비고", "2027-05-03 | 인천 → 프랑크푸르트 LH713 | 출국", "2027-05-04 | 현지 미팅 |", "2027-05-05 ~ 2027-05-08 | 호텔 숙박 (4박) | 시내 호텔", "2027-05-10 | 귀국 |"}},
		},
	},
	{
		name: "loan terms", format: "xlsx", language: "en",
		prompt: "Build a loan summary spreadsheet: principal USD 250,000, annual interest rate 6.25%, repaid in 60 monthly installments.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"Loan Summary", "Principal | 250000", "Annual interest rate | 6.25%", "Number of monthly payments | 60", "Monthly payment | 4862.36", "Total interest | 41741.60"}},
			{name: "rate differs", lines: []string{"Loan Summary", "Principal | 250000", "Annual interest rate | 6.75%", "Number of monthly payments | 60", "Monthly payment | 4920.86", "Total interest | 45251.60"}},
		},
	},
	{
		name: "maintenance contract", format: "pdf", language: "en",
		prompt: "Prepare a maintenance contract PDF with Coastal Freight LLC: quarterly inspections, emergency response within 8 hours, fee USD 3,600 per quarter.",
		variants: []documentVariant{
			{name: "matching", isFaithful: true, lines: []string{"EQUIPMENT MAINTENANCE CONTRACT", "Customer: Coastal Freight LLC", "1. Scope. Provider inspects the equipment once every quarter.", "2. Emergencies. Provider responds to an emergency call within eight (8) hours.", "3. Fees. Customer pays USD 3,600 per quarter, invoiced in advance.", "4. Term. This contract renews yearly unless either party gives notice."}},
			{name: "party differs", lines: []string{"EQUIPMENT MAINTENANCE CONTRACT", "Customer: Coastline Freight Inc.", "1. Scope. Provider inspects the equipment once every quarter.", "2. Emergencies. Provider responds to an emergency call within eight (8) hours.", "3. Fees. Customer pays USD 3,600 per quarter, invoiced in advance.", "4. Term. This contract renews yearly unless either party gives notice."}},
		},
	},
}

func writeLiveDocument(t *testing.T, filePath string, format string, lines []string) {
	t.Helper()
	switch format {
	case "docx":
		body := ""
		for _, line := range lines {
			body += wordParagraph(line)
		}
		writeZipDocument(t, filePath, map[string]string{"word/document.xml": `<w:document ` + wordNamespace + `><w:body>` + body + `</w:body></w:document>`})
	case "xlsx":
		writeZipDocument(t, filePath, liveWorkbookEntries(lines))
	case "pdf":
		writeLivePDF(t, filePath, lines)
	}
}

func liveWorkbookEntries(lines []string) map[string]string {
	rows := ""
	for rowIndex, line := range lines {
		cells := ""
		for columnIndex, value := range strings.Split(line, " | ") {
			cells += fmt.Sprintf(`<c r="%c%d" t="inlineStr"><is><t>%s</t></is></c>`, 'A'+columnIndex, rowIndex+1, strings.TrimSpace(value))
		}
		rows += fmt.Sprintf(`<row r="%d">%s</row>`, rowIndex+1, cells)
	}
	return map[string]string{
		"xl/workbook.xml":            `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Sheet1" sheetId="1" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Target="worksheets/sheet1.xml"/></Relationships>`,
		"xl/worksheets/sheet1.xml":   `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>` + rows + `</sheetData></worksheet>`,
	}
}

func writeLivePDF(t *testing.T, filePath string, lines []string) {
	t.Helper()
	glyphs := uniqueRunes(strings.Join(lines, ""))
	fixture := newPDFFixture(t)
	font := fixture.compositeFont(glyphs, true)
	content := []string{}
	for index, line := range lines {
		content = append(content, fmt.Sprintf("BT /F1 11 Tf 1 0 0 1 72 %d Tm <%s> Tj ET", 760-index*18, glyphCodes(glyphs, line)))
	}
	built := fixture.pageWith(fmt.Sprintf("/F1 %d 0 R", font), strings.Join(content, "\n"))
	content2, errorValue := os.ReadFile(built)
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(filePath, content2, 0o600); errorValue != nil {
		t.Fatal(errorValue)
	}
}

func uniqueRunes(text string) string {
	seen := map[rune]bool{}
	unique := []rune{}
	for _, character := range text {
		if !seen[character] {
			seen[character] = true
			unique = append(unique, character)
		}
	}
	return string(unique)
}

func deliveredLiveDocument(filename string) []turnObservation {
	devicePath := "/workspace/private/people/person-1/documents/" + filename
	commandInput, _ := json.Marshal(map[string]string{"command": "/opt/tools/office create ~/documents/" + filename + " spec.json"})
	deliverData, _ := json.Marshal(map[string]any{"deliveredPaths": []string{devicePath}, "attachmentCount": 1})
	return []turnObservation{
		{ObservationID: "obs-001", Action: "continue", Tool: toolcontract.BashToolName, ToolInput: commandInput, Output: toolcontract.ToolOutput{Content: `{"completed":true,"exitCode":0}`, Data: json.RawMessage(`{"completed":true,"exitCode":0,"stdout":"created","stderr":""}`)}},
		{ObservationID: "obs-002", Action: "continue", Tool: toolcontract.FileDeliverToolName, Output: toolcontract.ToolOutput{Content: "files delivered", Data: deliverData}, Effects: []toolcontract.ResourceEffect{{ObjectType: "file", Effect: "attached", Path: devicePath}}, Attachments: []toolcontract.FileAttachment{{Filename: filename, DevicePath: devicePath, SizeBytes: 20480}}},
	}
}

func TestLiveChangeCheckJudgesADeliveredDocumentByItsText(t *testing.T) {
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
	workspaceRootPath := t.TempDir()
	documentsPath := filepath.Join(workspaceRootPath, "private", "people", "person-1", "documents")
	if errorValue := os.MkdirAll(documentsPath, 0o700); errorValue != nil {
		t.Fatal(errorValue)
	}
	attempts := 3
	wrong := map[string]int{}
	for caseIndex, liveCase := range documentLiveCases {
		for attempt := 0; attempt < attempts; attempt++ {
			request := AgentTurnRequest{Prompt: liveCase.prompt, ToolSet: kernelFileToolSet()}
			expected, _ := services.runner.defineExpectedChanges(context.Background(), "live", request)
			for variantIndex, variant := range liveCase.variants {
				filename := fmt.Sprintf("case-%d-%d-%d.%s", caseIndex, variantIndex, attempt, liveCase.format)
				writeLiveDocument(t, filepath.Join(documentsPath, filename), liveCase.format, variant.lines)
				for _, mode := range []string{"before", "after"} {
					request.WorkspaceRootPath = ""
					if mode == "after" {
						request.WorkspaceRootPath = workspaceRootPath
					}
					check, errorValue := checkExpectedChanges(context.Background(), decisionModel, request, expected, deliveredLiveDocument(filename))
					if errorValue != nil {
						t.Fatal(errorValue)
					}
					isPassed := len(check.Unmet) == 0
					verdict := "ok"
					if isPassed != variant.isFaithful {
						verdict = "WRONG"
						wrong[mode]++
					}
					scores, _ := json.Marshal(check.CarriedOut)
					t.Logf("ROW|%s|%s|%s|%s|%s|#%d|passed=%v|%s|%s|expected=%d", mode, liveCase.name, liveCase.format, liveCase.language, variant.name, attempt, isPassed, verdict, scores, len(expected))
				}
			}
		}
	}
	t.Logf("wrong before=%d after=%d", wrong["before"], wrong["after"])
}
