//go:build llmeval

package intake

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/intake/intaketest"
	"github.com/yeomyeonggeori/bluecollar/turnclassification"
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
	"github.com/yeomyeonggeori/blueprotocol/evaltest"
	"github.com/yeomyeonggeori/blueprotocol/model/openaicompatible"
)

const (
	routerEvaluationModel          = "z-ai/glm-5.3-flash"
	routerEvaluationEndpoint       = "https://openrouter.ai/api/v1"
	routerEvaluationRepeatsName    = "BLUECOLLAR_ROUTER_EVAL_REPEATS"
	routerEvaluationArmName        = "BLUECOLLAR_ROUTER_EVAL_ARM"
	routerEvaluationExchangesName  = "BLUECOLLAR_ROUTER_EVAL_EXCHANGES"
	routerEvaluationConcurrency    = 8
	routerEvaluationDefaultRepeats = 3
)

var recordedRouterInputs = []string{
	"견적서를 pdf로 만들어 주세요.\n수신: 주식회사 샘플유통 박예시 과장님\n품목\n- 창고 관리 시스템 라이선스 (연간) 1식 4,800,000원\n- 현장 설치 및 초기 설정 1식 1,200,000원\n- 바코드 스캐너 12대, 대당 185,000원\n부가세 별도, 견적 유효기간 30일, 납기는 발주 후 3주입니다.",
	"투자자에게 보낼 IR 피치덱을 pptx로 만들어 주세요. 10~12장 정도로요.\n회사: 예시로보틱스 (물류 창고용 자율 이동 로봇을 월 구독으로 제공). 대표 이샘플, CFO 박예시, CTO 최견본.\n이번 라운드: 시리즈 A 30억 원 유치 목표.\n고객: 유료 고객사 23곳, 월 구독 유지율 96%.\n재무 실적과 전망 (단위: 백만 원)\n- 2023년 매출 420, 영업이익 -1,850\n- 2024년 매출 1,380, 영업이익 -1,240\n- 2025년 매출 3,100, 영업이익 -310\n- 2026년(전망) 매출 6,800, 영업이익 920\n자금 사용 계획: 연구개발 45%, 영업·마케팅 30%, 운영 25%.\n재무표 한 장과, 매출·영업이익 추이 차트(손실 구간과 2026년 전망이 구분되게)를 꼭 넣어 주세요. 문의처는 ir@example.com 입니다.",
	"Please make a product strategy deck as a pptx, about 10 to 12 slides, for our leadership offsite.\nProduct: Ledgerline, a bookkeeping app for independent cafés. Owner: Gyeonbon Choi (Head of Product). Presenter: Yesi Park.\nWhere we are: 4,200 paying cafés at the end of Q3 2026 (3,100 in Q1 and 3,500 in Q2), monthly churn 3.1%, NPS 41.\nInclude a chart of paying cafés by quarter and a roadmap slide.",
	"사내 3분기 업무 리뷰 발표자료를 pptx로 만들어 주세요. 10장 안팎, 분기 타임라인 한 장과 항목마다 어울리는 아이콘을 넣어 주세요. 발표자는 이샘플입니다.",
	"청구서를 pdf로 만들어 주세요.\n청구 대상: 주식회사 샘플유통 (담당 박예시 과장, billing@example.com)\n9월분 내역\n- 창고 관리 시스템 월 구독 1식 650,000원\n- 추가 사용자 계정 8개, 개당 15,000원\n부가세 10% 포함해서 청구하고, 지급 기한은 10월 31일입니다.",
	"어제 회의 회의록을 docx로 정리해 주세요.\n일시: 2026년 10월 3일 오후 2시~3시 10분\n참석: 이샘플, 박예시, 최견본\n장소는 기억이 안 나고, 다음 회의 날짜는 아직 안 정했어요.",
	"2026년 월별 매출 실적 엑셀 파일을 만들어 주세요. 월별 표, 분기별 합계, 월별 매출 차트를 넣어 주세요.\n9월 실적은 아직 마감 전이라 없습니다.",
	"신규 CRM 도입 검토 보고서를 docx로 써 주세요. 보고자 박예시, 보고 대상 이샘플 대표입니다.",
	"거래처에 보낼 사무실 이전 안내문을 pdf로 만들어 주세요.\n이전일: 2026년 11월 9일 (월)\n새 주소: 서울특별시 예시구 샘플로 12, 견본빌딩 7층\n대표 전화와 메일은 그대로 유지됩니다 (contact@example.com).",
	"첨부한 회사 출장비 정산서 양식에 첨부한 출장 메모 내용을 채워서 docx로 주세요. 양식 모양은 그대로 두세요.",
}

type routerEvaluationOutcome struct {
	Input            string `json:"input"`
	Error            string `json:"error,omitempty"`
	Route            string `json:"route,omitempty"`
	FallbackReason   string `json:"routingFallbackReason,omitempty"`
	DecisionCalls    int    `json:"decisionCalls"`
	WordsCalls       int    `json:"wordsCalls"`
	ProseAnswers     int    `json:"proseAnswers"`
	ContentAnswers   int    `json:"contentAnswers"`
	ElapsedMillisecs int64  `json:"elapsedMillisecs"`
}

func TestTurnRouterRoutesRealInputsWhateverChannelTheModelAnswersIn(t *testing.T) {
	apiKey := evaltest.RequireInput(t, "OPENROUTER_API_KEY", "run it under monkeys run @standalone")
	repeats := routerEvaluationRepeats(t)
	inputs := routerEvaluationInputs()
	exchanges := &exchangeJournal{path: strings.TrimSpace(os.Getenv(routerEvaluationExchangesName))}
	arm := strings.TrimSpace(os.Getenv(routerEvaluationArmName))

	outcomes := make([]routerEvaluationOutcome, len(inputs)*repeats)
	slots := make(chan struct{}, routerEvaluationConcurrency)
	var waitGroup sync.WaitGroup
	for index := range outcomes {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			outcomes[index] = routeOneInput(t, apiKey, arm, exchanges, inputs[index%len(inputs)])
		}(index)
	}
	waitGroup.Wait()
	reportRouterEvaluation(t, arm, outcomes)
}

func routerEvaluationRepeats(t *testing.T) int {
	text := strings.TrimSpace(os.Getenv(routerEvaluationRepeatsName))
	if text == "" {
		return routerEvaluationDefaultRepeats
	}
	repeats, errorValue := strconv.Atoi(text)
	if errorValue != nil || repeats < 1 {
		t.Fatalf("%s must be a positive whole number, got %q", routerEvaluationRepeatsName, text)
	}
	return repeats
}

func routerEvaluationInputs() []string {
	inputs := append([]string{}, recordedRouterInputs...)
	for _, benchmark := range benchmarkCases {
		inputs = append(inputs, benchmark.task)
	}
	return inputs
}

func routeOneInput(t *testing.T, apiKey string, arm string, exchanges *exchangeJournal, input string) routerEvaluationOutcome {
	transport := &armTransport{arm: arm, exchanges: exchanges}
	provider, errorValue := openaicompatible.Endpoint{URL: routerEvaluationEndpoint, ModelName: routerEvaluationModel, APIKey: apiKey, ProviderSort: "throughput", ReasoningEffort: "low"}.Provider()
	if errorValue != nil {
		t.Fatal(errorValue)
	}
	provider.UseHTTPClient(&http.Client{Transport: transport, Timeout: 2 * time.Minute})
	decisionModel := &intaketest.LanguageModelDecisionModel{
		LanguageModel: provider,
		ModelName:     routerEvaluationModel,
	}
	turnRouter := NewTurnRouter(provider, NewDecisionPlanner(decisionModel, nil), agentcontract.IntakeOptions{IsEnabled: true, DefaultTaskLevel: agentcontract.TaskLevelLow})
	startedAt := time.Now()
	decision, planError := turnRouter.Plan(context.Background(), agentcontract.AgentRequest{
		Prompt:           input,
		ConversationType: "direct",
		ResponseLanguage: "ko",
		TurnStartedAt:    time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
		EnvironmentNow:   time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC),
		Company:          agentcontract.CompanyContext{Name: "여명거리", TimeZone: "Asia/Seoul"},
	})
	outcome := transport.outcome()
	outcome.Input = input
	outcome.ElapsedMillisecs = time.Since(startedAt).Milliseconds()
	if planError != nil {
		outcome.Error = planError.Error()
		return outcome
	}
	outcome.Route = string(decision.Route)
	outcome.FallbackReason = routingFallbackReasonOf(decision)
	return outcome
}

func routingFallbackReasonOf(decision turnclassification.TurnDecision) string {
	document, _ := json.Marshal(decision)
	var fields map[string]any
	_ = json.Unmarshal(document, &fields)
	reason, _ := fields["routingFallbackReason"].(string)
	return reason
}

func reportRouterEvaluation(t *testing.T, arm string, outcomes []routerEvaluationOutcome) {
	failed, malformed, fellBack, decisionCalls, wordsCalls, prose, content := 0, 0, 0, 0, 0, 0, 0
	for _, outcome := range outcomes {
		decisionCalls += outcome.DecisionCalls
		wordsCalls += outcome.WordsCalls
		prose += outcome.ProseAnswers
		content += outcome.ContentAnswers
		if outcome.Error != "" {
			failed++
			if strings.HasPrefix(outcome.Error, "turn router words:") && strings.Contains(outcome.Error, "with prose instead of calling the schema") {
				malformed++
			}
			t.Logf("failed: %s :: %.160s", strings.SplitN(outcome.Input, "\n", 2)[0], outcome.Error)
		}
		if outcome.FallbackReason != "" {
			fellBack++
			t.Logf("fell back: %s :: %.160s", strings.SplitN(outcome.Input, "\n", 2)[0], outcome.FallbackReason)
		}
	}
	calls := decisionCalls + wordsCalls
	t.Logf("arm %q: %d routings, %d failed the task, %d fell back to the default route", arm, len(outcomes), failed, fellBack)
	t.Logf("%d structured calls (%d decision, %d words): %d answered without the tool call (%.1f%%), %d of those carried the answer as message content, %d as prose (%.1f%% of calls)", calls, decisionCalls, wordsCalls, prose+content, percentOf(prose+content, calls), content, prose, percentOf(prose, calls))
	t.Logf("%d of %d routings failed on a malformed answer, %d on something else (the stand-in decision model, which production replaces with the decision endpoint, refusing an answer the model left incomplete)", malformed, len(outcomes), failed-malformed)
	if malformed > 0 {
		t.Errorf("%d of %d routings failed the task on a malformed answer (%.1f%%)", malformed, len(outcomes), percentOf(malformed, len(outcomes)))
	}
}

func percentOf(count int, total int) float64 {
	if total == 0 {
		return 0
	}
	return 100 * float64(count) / float64(total)
}

type armTransport struct {
	arm       string
	exchanges *exchangeJournal

	mutex          sync.Mutex
	decisionCalls  int
	wordsCalls     int
	proseAnswers   int
	contentAnswers int
}

func (transport *armTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	requestBody, errorValue := io.ReadAll(request.Body)
	if errorValue != nil {
		return nil, errorValue
	}
	requestBody = armedRequestBody(transport.arm, requestBody)
	request.Body = io.NopCloser(bytes.NewReader(requestBody))
	request.ContentLength = int64(len(requestBody))
	response, errorValue := http.DefaultTransport.RoundTrip(request)
	if errorValue != nil {
		return nil, errorValue
	}
	responseBody, errorValue := io.ReadAll(response.Body)
	response.Body.Close()
	if errorValue != nil {
		return nil, errorValue
	}
	response.Body = io.NopCloser(bytes.NewReader(responseBody))
	transport.tally(requestBody, responseBody)
	transport.exchanges.append(transport.arm, requestBody, responseBody)
	return response, nil
}

func armedRequestBody(arm string, requestBody []byte) []byte {
	if arm == "" {
		return requestBody
	}
	var document map[string]any
	if json.Unmarshal(requestBody, &document) != nil {
		return requestBody
	}
	routing, _ := document["provider"].(map[string]any)
	if routing == nil {
		routing = map[string]any{}
	}
	routing["require_parameters"] = true
	document["provider"] = routing
	if arm == "response_format" {
		tools, _ := document["tools"].([]any)
		if len(tools) == 1 {
			function := tools[0].(map[string]any)["function"].(map[string]any)
			document["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": function["name"], "strict": true, "schema": function["parameters"]}}
			delete(document, "tools")
			delete(document, "tool_choice")
		}
	}
	armed, errorValue := json.Marshal(document)
	if errorValue != nil {
		return requestBody
	}
	return armed
}

func (transport *armTransport) tally(requestBody []byte, responseBody []byte) {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	if bytes.Contains(requestBody, []byte(`\"route\"`)) || bytes.Contains(requestBody, []byte(`"route":`)) {
		transport.decisionCalls++
	} else {
		transport.wordsCalls++
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string            `json:"content"`
				ToolCalls []json.RawMessage `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if json.Unmarshal(responseBody, &decoded) != nil || len(decoded.Choices) == 0 || len(decoded.Choices[0].Message.ToolCalls) > 0 {
		return
	}
	var object map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(decoded.Choices[0].Message.Content)), &object) == nil {
		transport.contentAnswers++
		return
	}
	transport.proseAnswers++
}

func (transport *armTransport) outcome() routerEvaluationOutcome {
	transport.mutex.Lock()
	defer transport.mutex.Unlock()
	return routerEvaluationOutcome{DecisionCalls: transport.decisionCalls, WordsCalls: transport.wordsCalls, ProseAnswers: transport.proseAnswers, ContentAnswers: transport.contentAnswers}
}

type exchangeJournal struct {
	path  string
	mutex sync.Mutex
}

func (journal *exchangeJournal) append(arm string, requestBody []byte, responseBody []byte) {
	if journal.path == "" {
		return
	}
	line, errorValue := json.Marshal(map[string]any{"arm": arm, "request": json.RawMessage(requestBody), "response": json.RawMessage(responseBody)})
	if errorValue != nil {
		return
	}
	journal.mutex.Lock()
	defer journal.mutex.Unlock()
	file, errorValue := os.OpenFile(journal.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if errorValue != nil {
		return
	}
	defer file.Close()
	fmt.Fprintln(file, string(line))
}
