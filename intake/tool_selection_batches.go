package intake

import (
	"encoding/json"
	"math"
	"sort"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type toolSelectionRun struct {
	providerID string
	tools      []decisionTool
	byteCount  int
}

func toolSelectionBatches(tools []decisionTool, byteCountByToolName map[string]int, batchCount int) [][]decisionTool {
	if batchCount <= 1 {
		return [][]decisionTool{tools}
	}
	runs := toolSelectionRunsNoLargerThan(tools, byteCountByToolName, batchShareByteCount(tools, byteCountByToolName, batchCount))
	batches := make([][]decisionTool, batchCount)
	batchByteCounts := make([]int, batchCount)
	for _, run := range runsRankedLargestFirst(runs) {
		lightestIndex := indexOfLightestBatch(batchByteCounts)
		batches[lightestIndex] = append(batches[lightestIndex], run.tools...)
		batchByteCounts[lightestIndex] += run.byteCount
	}
	return batchesInNameOrder(batches)
}

func batchShareByteCount(tools []decisionTool, byteCountByToolName map[string]int, batchCount int) int {
	totalByteCount := 0
	for _, tool := range tools {
		totalByteCount += byteCountByToolName[tool.Name]
	}
	return (totalByteCount + batchCount - 1) / batchCount
}

func toolSelectionRunsNoLargerThan(tools []decisionTool, byteCountByToolName map[string]int, shareByteCount int) []toolSelectionRun {
	runs := []toolSelectionRun{}
	for _, providerTools := range toolsGroupedByProvider(tools) {
		runs = append(runs, providerRunsNoLargerThan(providerTools, byteCountByToolName, shareByteCount)...)
	}
	return runs
}

func toolsGroupedByProvider(tools []decisionTool) [][]decisionTool {
	toolsByProviderID := map[string][]decisionTool{}
	providerIDs := []string{}
	for _, tool := range tools {
		providerID := providerGroupKey(tool)
		if _, isKnownProvider := toolsByProviderID[providerID]; !isKnownProvider {
			providerIDs = append(providerIDs, providerID)
		}
		toolsByProviderID[providerID] = append(toolsByProviderID[providerID], tool)
	}
	sort.Strings(providerIDs)
	groups := make([][]decisionTool, 0, len(providerIDs))
	for _, providerID := range providerIDs {
		groups = append(groups, toolsByProviderID[providerID])
	}
	return groups
}

func providerGroupKey(tool decisionTool) string {
	if tool.providerID == "" {
		return "tool:" + tool.Name
	}
	return "provider:" + tool.providerID
}

func providerRunsNoLargerThan(providerTools []decisionTool, byteCountByToolName map[string]int, shareByteCount int) []toolSelectionRun {
	runs := []toolSelectionRun{}
	currentRun := toolSelectionRun{providerID: providerGroupKey(providerTools[0])}
	for _, tool := range providerTools {
		toolByteCount := byteCountByToolName[tool.Name]
		if len(currentRun.tools) > 0 && currentRun.byteCount+toolByteCount > shareByteCount {
			runs = append(runs, currentRun)
			currentRun = toolSelectionRun{providerID: currentRun.providerID}
		}
		currentRun.tools = append(currentRun.tools, tool)
		currentRun.byteCount += toolByteCount
	}
	return append(runs, currentRun)
}

func runsRankedLargestFirst(runs []toolSelectionRun) []toolSelectionRun {
	rankedRuns := append([]toolSelectionRun{}, runs...)
	sort.SliceStable(rankedRuns, func(leftIndex int, rightIndex int) bool {
		leftRun, rightRun := rankedRuns[leftIndex], rankedRuns[rightIndex]
		if leftRun.byteCount != rightRun.byteCount {
			return leftRun.byteCount > rightRun.byteCount
		}
		return leftRun.tools[0].Name < rightRun.tools[0].Name
	})
	return rankedRuns
}

func indexOfLightestBatch(batchByteCounts []int) int {
	lightestIndex := 0
	for index, byteCount := range batchByteCounts {
		if byteCount < batchByteCounts[lightestIndex] {
			lightestIndex = index
		}
	}
	return lightestIndex
}

func batchesInNameOrder(batches [][]decisionTool) [][]decisionTool {
	orderedBatches := [][]decisionTool{}
	for _, batch := range batches {
		if len(batch) == 0 {
			continue
		}
		sort.SliceStable(batch, func(leftIndex int, rightIndex int) bool {
			return batch[leftIndex].Name < batch[rightIndex].Name
		})
		orderedBatches = append(orderedBatches, batch)
	}
	sort.SliceStable(orderedBatches, func(leftIndex int, rightIndex int) bool {
		return orderedBatches[leftIndex][0].Name < orderedBatches[rightIndex][0].Name
	})
	return orderedBatches
}

func toolSelectionByteCounts(builder questionBuilder, messageKeys []string, tools []decisionTool) map[string]int {
	byteCountByToolName := make(map[string]int, len(tools))
	for _, tool := range tools {
		toolByteCount := jsonByteCount(tool)
		for _, messageKey := range messageKeys {
			toolByteCount += len(toolQuestionName(messageKey, tool.Name)) + jsonByteCount(builder.likelyToolQuestion(messageKey, tool.Name))
		}
		byteCountByToolName[tool.Name] = toolByteCount
	}
	return byteCountByToolName
}

func smallestBatchCountThatFits(fixedByteCount int, byteCountByToolName map[string]int) int {
	totalToolByteCount := 0
	for _, byteCount := range byteCountByToolName {
		totalToolByteCount += byteCount
	}
	availableByteCount := decisionRequestByteBudget - fixedByteCount
	if availableByteCount <= 0 {
		return len(byteCountByToolName)
	}
	return (totalToolByteCount + availableByteCount - 1) / availableByteCount
}

func jsonByteCount(value any) int {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return math.MaxInt / 2
	}
	return len(document)
}

func toolNamesOf(tools []decisionTool) []string {
	toolNames := make([]string, 0, len(tools))
	for _, tool := range tools {
		toolNames = append(toolNames, tool.Name)
	}
	return toolNames
}

func batchByteCounts(requests []model.DecisionRequest) []int {
	byteCounts := make([]int, 0, len(requests))
	for _, request := range requests {
		byteCounts = append(byteCounts, decisionRequestByteCount(request))
	}
	return byteCounts
}
