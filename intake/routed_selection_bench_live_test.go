//go:build llmeval

package intake

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/model/decisions"
)

func TestRoutedSelectionReachesEveryToolTheWorkNeeds(t *testing.T) {
	toolSet, toolNames := catalogToolSet(t)
	endpoint, errorValue := decisions.EndpointFromEnvironment()
	if errorValue != nil {
		t.Skipf("decisions endpoint: %v", errorValue)
	}
	planner := NewDecisionPlanner(endpoint.DecisionModel(), nil, func() float64 { return 1 })

	selectedByCase := make([][]string, len(benchmarkCases))
	countByCase := make([]agentcontract.ExpectedToolCount, len(benchmarkCases))
	routeByCase := make([]agentcontract.TurnRoute, len(benchmarkCases))
	startedAt := time.Now()
	var waitGroup sync.WaitGroup
	for index, benchmark := range benchmarkCases {
		waitGroup.Add(1)
		go func(index int, task string) {
			defer waitGroup.Done()
			request := addressedDecisionRequest(task)
			request.ToolSet = toolSet
			request.CallableToolNames = toolNames
			decisions, errorValue := planner.Decide(context.Background(), request, &agentcontract.IntakeCallLedger{})
			if errorValue != nil {
				t.Errorf("%q: %v", task, errorValue)
				return
			}
			selectedByCase[index] = decisions.Messages[0].TurnFields.InitialToolNames
			countByCase[index] = decisions.Messages[0].TurnFields.ExpectedToolCount
			routeByCase[index] = decisions.Messages[0].TurnFields.Route
		}(index, benchmark.task)
	}
	waitGroup.Wait()
	elapsed := time.Since(startedAt)

	recalled, expected, paletteTotal, noise, noNeed := 0, 0, 0, 0, 0
	countedByAnswer := map[agentcontract.ExpectedToolCount]int{}
	countedByRoute := map[agentcontract.TurnRoute]int{}
	for index, benchmark := range benchmarkCases {
		selected := selectedByCase[index]
		countedByAnswer[countByCase[index]]++
		countedByRoute[routeByCase[index]]++
		paletteTotal += len(selected)
		selectedByName := map[string]bool{}
		for _, name := range selected {
			selectedByName[name] = true
		}
		for _, wanted := range benchmark.expectedTools {
			expected++
			if selectedByName[wanted] {
				recalled++
				continue
			}
			t.Logf("  놓침 %q → %s (판정 %s, 선택 %v)", benchmark.task, wanted, countByCase[index], selected)
		}
		if len(benchmark.expectedTools) == 0 {
			noNeed++
			if len(selected) > 0 {
				noise++
			}
		}
	}
	for route, count := range countedByRoute {
		t.Logf("  라우트 %-16s %d", route, count)
	}
	t.Logf("프로덕션 라우팅 %d/%-3d 팔레트 %.1f 노이즈 %d/%d · none %d · one %d · several %d · %s",
		recalled, expected, float64(paletteTotal)/float64(len(benchmarkCases)), noise, noNeed,
		countedByAnswer[agentcontract.ExpectedToolCountNone],
		countedByAnswer[agentcontract.ExpectedToolCountOne],
		countedByAnswer[agentcontract.ExpectedToolCountSeveral], elapsed.Round(time.Millisecond))
}
