//go:build llmeval

package intake

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

const catalogPathEnvironmentName = "BLUECOLLAR_SELECTION_BENCH_CATALOG"
const reportPathEnvironmentName = "BLUECOLLAR_SELECTION_BENCH_REPORT"

type benchmarkCase struct {
	task          string
	expectedTools []string
}

var benchmarkCases = []benchmarkCase{
	{"지난주 근태 기록을 정리해서 알려줘", []string{"attendance_list"}},
	{"어제 출근 시간 잘못 찍힌 거 고쳐줘", []string{"attendance_update"}},
	{"우리 회사 근무시간 정책이 어떻게 되지", []string{"attendance_work_policy_get"}},
	{"내일 오후 3시에 팀 회의 잡아줘", []string{"event_add"}},
	{"이번 주 일정 뭐 있어", []string{"event_list"}},
	{"금요일 회의 취소해줘", []string{"event_delete"}},
	{"이번 달 휴가 신청 현황 확인", []string{"leave_list"}},
	{"내 연차 며칠 남았지", []string{"leave_balance"}},
	{"다음 주 월요일 연차 신청할게", []string{"leave_request"}},
	{"김샘플 님 휴가 신청 승인해줘", []string{"leave_decide"}},
	{"김샘플 님한테 회의록 보내줘", []string{"message_send"}},
	{"어제 공지 내용 뭐였지 찾아줘", []string{"message_search"}},
	{"우리 회사 거래처 목록 좀 보자", []string{"crm_organization_list"}},
	{"이 고객 건 수주 단계로 옮겨줘", []string{"crm_opportunity_move"}},
	{"신규 거래처 등록해줘", []string{"crm_organization_add"}},
	{"담당자 연락처 새로 추가해줘", []string{"crm_contact_add"}},
	{"그 거래 금액을 3천만원으로 고쳐줘", []string{"crm_opportunity_update"}},
	{"박예시 님을 회사에 초대해줘", []string{"person_invite"}},
	{"최견본 님 직책을 팀장으로 바꿔줘", []string{"person_update"}},
	{"우리 회사 직원 명단 보여줘", []string{"person_list"}},
	{"매주 월요일 아침에 주간보고 리마인더 돌려줘", []string{"schedule_create"}},
	{"그 반복 알림 그만해줘", []string{"schedule_cancel"}},
	{"받은 메일 중에 계약 관련된 것만 찾아줘", []string{"mail_message_search"}},
	{"받은편지함 메일 목록 보여줘", []string{"mail_message_list"}},
	{"이 메일 읽음 처리해줘", []string{"mail_message_mark"}},
	{"이번 주 할 일 목록 보여줘", []string{"task_list"}},
	{"이 업무 완료로 바꿔줘", []string{"task_update"}},
	{"새 업무 하나 등록해줘", []string{"task_add"}},
	{"개발팀에 김샘플 님 배치해줘", []string{"person_update"}},
	{"팀 목록 보여줘", []string{"team_list"}},
	{"새 조직 하나 만들어줘", []string{"team_add"}},
	{"회사 규정 문서 어디 있지 찾아줘", []string{"company_document_search"}},
	{"이 파일 회사 자료실에 올려줘", []string{"company_document_upload"}},
	{"올해 공휴일 등록해줘", []string{"company_holiday_add"}},
	{"우리 회사 이번 분기 매출 지표 기록해줘", []string{"company_metric_record"}},
	{"경쟁사 최근 소식 검색해봐", []string{"web_search"}},
	{"이 링크 내용 읽고 요약해줘", []string{"web_fetch"}},
	{"워크스페이스에 만들어둔 소개 사이트 게시해줘", []string{"site_serve"}},
	{"이 대화 알림 꺼줘", []string{"conversation_mute"}},
	{"내일 오후 2시 회의 잡고 참석자들한테 공지해줘", []string{"event_add", "message_send"}},
	{"지난주 근태 정리해서 김샘플 님한테 보내줘", []string{"attendance_list", "message_send"}},
	{"신규 거래처 등록하고 담당자도 같이 추가해줘", []string{"crm_organization_add", "crm_contact_add"}},
	{"이 링크 읽고 요약해서 업무로 등록해줘", []string{"web_fetch", "task_add"}},
	{"김샘플 님 휴가 신청 승인하고 본인한테 알려줘", []string{"leave_decide", "message_send"}},
	{"이번 주 일정이랑 할 일 같이 보여줘", []string{"event_list", "task_list"}},
	{"경쟁사 소식 검색해서 회사 자료실에 올려줘", []string{"web_search", "company_document_upload"}},
	{"이번 분기 매출 지표 기록하고 팀에 공유해줘", []string{"company_metric_record", "message_send"}},
	{"박예시 님 초대하고 개발팀으로 배치해줘", []string{"person_invite", "person_update"}},
	{"받은 메일에서 계약 건 찾아서 업무로 만들어줘", []string{"mail_message_search", "task_add"}},
	{"휴가 신청하고 팀장님한테 알려줘", []string{"leave_request", "message_send"}},
	{"그 거래 금액 수정하고 담당자한테 메일 보내줘", []string{"crm_opportunity_update", "mail_message_send"}},
	{"다음 주 일정 확인하고 공휴일도 등록해줘", []string{"event_list", "company_holiday_add"}},
	{"직원 명단 뽑아서 회사 자료실에 올려줘", []string{"person_list", "company_document_upload"}},
	{"이 업무 완료 처리하고 결과 공유해줘", []string{"task_update", "message_send"}},
	{"근무시간 정책 확인해서 직원들한테 공지해줘", []string{"attendance_work_policy_get", "message_send"}},
	{"거래처 목록 보고 새 거래 하나 등록해줘", []string{"crm_organization_list", "crm_opportunity_add"}},
	{"메일에서 일정 찾아서 캘린더에 넣어줘", []string{"mail_message_search", "event_add"}},
	{"회사 규정 문서 찾아서 신입한테 보내줘", []string{"company_document_search", "message_send"}},
	{"이 링크 읽고 거래처 정보 업데이트해줘", []string{"web_fetch", "crm_organization_update"}},
	{"경쟁사 조사해서 문서로 정리하고 팀에 공유해줘", []string{"web_search", "company_document_upload", "message_send"}},
	{"김샘플 님 휴가 승인하고 일정에 등록하고 본인한테 알려줘", []string{"leave_decide", "event_add", "message_send"}},
	{"지난주 근태 뽑아서 지표로 기록하고 보고해줘", []string{"attendance_list", "company_metric_record", "message_send"}},
	{"신규 거래처 등록하고 담당자 추가하고 첫 미팅도 잡아줘", []string{"crm_organization_add", "crm_contact_add", "event_add"}},
	{"메일에서 계약 건 찾아서 업무 만들고 담당자한테 알려줘", []string{"mail_message_search", "task_add", "message_send"}},
	{"고마워요", nil},
	{"ㅇㅋ", nil},
	{"수고하셨습니다", nil},
	{"네 알겠습니다", nil},
}

type selectionPolicy struct {
	name       string
	countLimit int
	floor      float64
	useMean    bool
}

var selectionPolicies = []selectionPolicy{
	{"현재 (고정 0.3, 상한 12)", 12, likelyToolProbabilityThreshold, false},
	{"평균 + 바닥 0.05, 상한 12", 12, recordedToolProbabilityFloor, true},
	{"평균 + 바닥 0.05, 상한 없음", 0, recordedToolProbabilityFloor, true},
	{"평균 + 바닥 0.15, 상한 12", 12, 0.15, true},
}

type recordingDecisionModel struct {
	inner          model.DecisionModel
	mutex          sync.Mutex
	noulByQuestion map[string]float64
}

func (recorder *recordingDecisionModel) Decide(ctx context.Context, request model.DecisionRequest) (model.DecisionResponse, error) {
	response, errorValue := recorder.inner.Decide(ctx, request)
	if errorValue != nil {
		return response, errorValue
	}
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	for questionKey, answer := range response.Answers {
		recorder.noulByQuestion[questionKey] = answer.Noul
	}
	return response, nil
}

func (recorder *recordingDecisionModel) probabilityByToolName(toolNames []string) map[string]float64 {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	probabilities := map[string]float64{}
	for _, toolName := range toolNames {
		suffix := "." + agentcontract.IntakeQuestionPrefixTool + toolName
		for questionKey, noul := range recorder.noulByQuestion {
			if strings.HasSuffix(questionKey, suffix) {
				probabilities[toolName] = noul
				break
			}
		}
	}
	return probabilities
}

func applyPolicy(policy selectionPolicy, probabilityByToolName map[string]float64, toolNames []string) []string {
	threshold := policy.floor
	if policy.useMean {
		total := 0.0
		for _, toolName := range toolNames {
			total += probabilityByToolName[toolName]
		}
		mean := total / float64(len(toolNames))
		if mean > threshold {
			threshold = mean
		}
	}
	selected := []string{}
	for _, toolName := range toolNamesRankedByProbability(probabilityByToolName, toolNames) {
		if probabilityByToolName[toolName] < threshold {
			break
		}
		if policy.countLimit > 0 && len(selected) >= policy.countLimit {
			break
		}
		selected = append(selected, toolName)
	}
	return selected
}

func catalogToolSet(t *testing.T) (*toolcontract.ToolSet, []string) {
	catalogPath := strings.TrimSpace(os.Getenv(catalogPathEnvironmentName))
	if catalogPath == "" {
		t.Skipf("set %s to the generated capability-tools.json", catalogPathEnvironmentName)
	}
	document, errorValue := os.ReadFile(catalogPath)
	if errorValue != nil {
		t.Fatalf("read catalog: %v", errorValue)
	}
	var catalog struct {
		Tools []struct {
			Name            string `json:"name"`
			ModelName       string `json:"modelName"`
			Description     string `json:"description"`
			ModelVisibility string `json:"modelVisibility"`
		} `json:"tools"`
	}
	if errorValue := json.Unmarshal(document, &catalog); errorValue != nil {
		t.Fatalf("parse catalog: %v", errorValue)
	}
	toolNames := []string{}
	descriptionByName := map[string]string{}
	for _, tool := range catalog.Tools {
		if tool.ModelVisibility == "hidden" {
			continue
		}
		name := tool.ModelName
		if name == "" {
			name = tool.Name
		}
		toolNames = append(toolNames, name)
		descriptionByName[name] = tool.Description
	}
	sort.Strings(toolNames)
	toolSet := toolcontract.NewToolSet(toolNames)
	toolSet.AllowTestReplacement()
	for _, toolName := range toolNames {
		toolSet.RegisterBoundTool(toolcontract.BoundTool{
			Definition: toolcontract.ToolDefinition{
				ID:           "catalog:" + toolName,
				Name:         toolName,
				Description:  descriptionByName[toolName],
				Visibility:   toolcontract.ToolVisibilityModel,
				InputSchema:  json.RawMessage(`{"type":"object","properties":{}}`),
				OutputSchema: json.RawMessage(`{"type":"object","properties":{}}`),
			},
			Availability: toolcontract.ToolAvailability{Status: toolcontract.ToolAvailabilityAvailable},
			Handler: func(context.Context, toolcontract.ToolInvocation) (toolcontract.ToolResult, error) {
				return toolcontract.ToolResult{}, nil
			},
		})
	}
	return toolSet, toolNames
}

type caseOutcome struct {
	selectedCount int
	recalled      int
	expected      int
}

type caseDetail struct {
	Task          string   `json:"task"`
	ExpectedTools []string `json:"expectedTools"`
	MissedTools   []string `json:"missedTools,omitempty"`
	MissedRanks   []int    `json:"missedRanks,omitempty"`
	SelectedCount int      `json:"selectedCount"`
	TopTools      []string `json:"topTools,omitempty"`
}

func rankOf(toolName string, ranked []string) int {
	for index, name := range ranked {
		if name == toolName {
			return index + 1
		}
	}
	return 0
}

func TestToolSelectionPolicyBenchmark(t *testing.T) {
	endpoint, errorValue := decisions.EndpointFromEnvironment()
	if errorValue != nil {
		t.Skipf("%v", errorValue)
	}
	toolSet, toolNames := catalogToolSet(t)
	inCatalog := map[string]bool{}
	for _, toolName := range toolNames {
		inCatalog[toolName] = true
	}
	for _, benchmark := range benchmarkCases {
		for _, expected := range benchmark.expectedTools {
			if !inCatalog[expected] {
				t.Fatalf("%q expects %s, which the catalog does not serve, so no policy could ever reach it", benchmark.task, expected)
			}
		}
	}

	outcomesByPolicy := map[string][]caseOutcome{}
	caseDetails := []caseDetail{}
	startedAt := time.Now()

	for _, benchmark := range benchmarkCases {
		recorder := &recordingDecisionModel{inner: endpoint.DecisionModel(), noulByQuestion: map[string]float64{}}
		planner := NewDecisionPlanner(recorder, nil, nil)
		callLedger := &agentcontract.IntakeCallLedger{}
		if _, errorValue := planner.SelectToolNames(context.Background(), agentcontract.ToolSelectionNeed{
			Need:              benchmark.task,
			ToolSet:           toolSet,
			CallableToolNames: toolNames,
			CallLedger:        callLedger,
		}); errorValue != nil {
			t.Fatalf("%q: %v", benchmark.task, errorValue)
		}
		probabilities := recorder.probabilityByToolName(toolNames)
		if len(probabilities) == 0 {
			t.Fatalf("%q: the decision model answered no tool question", benchmark.task)
		}
		ranked := toolNamesRankedByProbability(probabilities, toolNames)
		for _, policy := range selectionPolicies {
			selected := applyPolicy(policy, probabilities, toolNames)
			recalled := 0
			for _, expected := range benchmark.expectedTools {
				for _, selectedName := range selected {
					if selectedName == expected {
						recalled++
						break
					}
				}
			}
			outcomesByPolicy[policy.name] = append(outcomesByPolicy[policy.name], caseOutcome{
				selectedCount: len(selected),
				recalled:      recalled,
				expected:      len(benchmark.expectedTools),
			})
			if policy.name != selectionPolicies[0].name {
				continue
			}
			detail := caseDetail{Task: benchmark.task, ExpectedTools: benchmark.expectedTools, SelectedCount: len(selected)}
			for index, toolName := range ranked {
				if index >= 5 {
					break
				}
				detail.TopTools = append(detail.TopTools, fmt.Sprintf("%s %.2f", toolName, probabilities[toolName]))
			}
			for _, expected := range benchmark.expectedTools {
				isSelected := false
				for _, selectedName := range selected {
					if selectedName == expected {
						isSelected = true
						break
					}
				}
				if !isSelected {
					detail.MissedTools = append(detail.MissedTools, expected)
					detail.MissedRanks = append(detail.MissedRanks, rankOf(expected, ranked))
				}
			}
			caseDetails = append(caseDetails, detail)
		}
	}

	elapsed := time.Since(startedAt).Round(time.Second)
	report := benchmarkReport{
		Model:        endpoint.ModelName,
		CaseCount:    len(benchmarkCases),
		CatalogCount: len(toolNames),
		ElapsedSecs:  int(elapsed.Seconds()),
		Cases:        caseDetails,
	}
	t.Logf("%d cases over %d catalog tools in %s\n", len(benchmarkCases), len(toolNames), elapsed)
	t.Log(fmt.Sprintf("%-30s %8s %8s %10s %10s", "policy", "recall", "empty", "avg size", "noise"))
	for _, policy := range selectionPolicies {
		outcomes := outcomesByPolicy[policy.name]
		recalled, expected, emptyHands, totalSize, noiseCases, noiseTotal := 0, 0, 0, 0, 0, 0
		for _, outcome := range outcomes {
			recalled += outcome.recalled
			expected += outcome.expected
			totalSize += outcome.selectedCount
			if outcome.expected > 0 && outcome.selectedCount == 0 {
				emptyHands++
			}
			if outcome.expected == 0 {
				noiseTotal++
				if outcome.selectedCount > 0 {
					noiseCases++
				}
			}
		}
		recall := 0.0
		if expected > 0 {
			recall = float64(recalled) / float64(expected) * 100
		}
		report.Policies = append(report.Policies, policyReport{
			Name:            policy.name,
			RecallPercent:   recall,
			RecalledTools:   recalled,
			ExpectedTools:   expected,
			EmptyHandCases:  emptyHands,
			AveragePalette:  float64(totalSize) / float64(len(outcomes)),
			NoiseCases:      noiseCases,
			NoNeedCaseCount: noiseTotal,
		})
		t.Log(fmt.Sprintf("%-30s %7.0f%% %8d %10.1f %10d",
			policy.name, recall, emptyHands, float64(totalSize)/float64(len(outcomes)), noiseCases))
	}
	writeReport(t, report)
}

type policyReport struct {
	Name            string  `json:"name"`
	RecallPercent   float64 `json:"recallPercent"`
	RecalledTools   int     `json:"recalledTools"`
	ExpectedTools   int     `json:"expectedTools"`
	EmptyHandCases  int     `json:"emptyHandCases"`
	AveragePalette  float64 `json:"averagePalette"`
	NoiseCases      int     `json:"noiseCases"`
	NoNeedCaseCount int     `json:"noNeedCaseCount"`
}

type benchmarkReport struct {
	Model        string         `json:"model"`
	CaseCount    int            `json:"caseCount"`
	CatalogCount int            `json:"catalogCount"`
	ElapsedSecs  int            `json:"elapsedSeconds"`
	Policies     []policyReport `json:"policies"`
	Cases        []caseDetail   `json:"cases"`
}

func writeReport(t *testing.T, report benchmarkReport) {
	reportPath := strings.TrimSpace(os.Getenv(reportPathEnvironmentName))
	if reportPath == "" {
		return
	}
	document, errorValue := json.MarshalIndent(report, "", "  ")
	if errorValue != nil {
		t.Fatalf("marshal report: %v", errorValue)
	}
	if errorValue := os.WriteFile(reportPath, document, 0o644); errorValue != nil {
		t.Fatalf("write report: %v", errorValue)
	}
	t.Logf("report written to %s", reportPath)
}
