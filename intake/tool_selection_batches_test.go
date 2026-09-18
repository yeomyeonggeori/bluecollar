package intake

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func syntheticCatalog(providerCount int, toolsPerProvider int) []syntheticTool {
	syntheticTools := []syntheticTool{}
	for providerIndex := range providerCount {
		providerID := "provider_" + strconv.Itoa(providerIndex)
		descriptionLength := 40 + providerIndex*30
		for toolIndex := range toolsPerProvider {
			syntheticTools = append(syntheticTools, syntheticTool{
				name:        providerID + "_tool_" + strconv.Itoa(toolIndex),
				providerID:  providerID,
				description: strings.Repeat("what this tool does. ", 1+descriptionLength/20),
			})
		}
	}
	return syntheticTools
}

func describedSyntheticTools(syntheticTools []syntheticTool) []decisionTool {
	toolNames := []string{}
	for _, syntheticTool := range syntheticTools {
		toolNames = append(toolNames, syntheticTool.name)
	}
	sort.Strings(toolNames)
	return decisionToolDescriptions(newProviderToolSet(syntheticTools), toolNames).tools
}

func toolByteCountsOf(tools []decisionTool) map[string]int {
	return toolSelectionByteCounts(newQuestionBuilder(addressedDecisionRequest("무엇이든")), []string{decisionMessageKey(0)}, tools)
}

func batchLoads(batches [][]decisionTool, byteCountByToolName map[string]int) []int {
	loads := []int{}
	for _, batch := range batches {
		load := 0
		for _, tool := range batch {
			load += byteCountByToolName[tool.Name]
		}
		loads = append(loads, load)
	}
	return loads
}

func providerOfBatches(batches [][]decisionTool, providerID string) []string {
	toolNames := []string{}
	for _, batch := range batches {
		for _, tool := range batch {
			if tool.providerID == providerID {
				toolNames = append(toolNames, tool.Name)
			}
		}
	}
	return toolNames
}

func TestEveryBatchCarriesAlmostTheSameBytesAsTheOthers(t *testing.T) {
	tools := describedSyntheticTools(syntheticCatalog(30, 50))
	byteCountByToolName := toolByteCountsOf(tools)

	for _, batchCount := range []int{2, 3, 7, 11} {
		batches := toolSelectionBatches(tools, byteCountByToolName, batchCount)
		shareByteCount := batchShareByteCount(tools, byteCountByToolName, batchCount)
		loads := batchLoads(batches, byteCountByToolName)
		smallestLoad, largestLoad := loads[0], loads[0]
		for _, load := range loads {
			smallestLoad = min(smallestLoad, load)
			largestLoad = max(largestLoad, load)
		}
		if largestLoad-smallestLoad > shareByteCount {
			t.Fatalf("expected %d batches within one share (%d bytes) of each other, got loads between %d and %d", batchCount, shareByteCount, smallestLoad, largestLoad)
		}
	}
}

func TestAProviderThatFitsABatchIsNeverSplitAcrossBatches(t *testing.T) {
	tools := describedSyntheticTools(syntheticCatalog(12, 10))
	byteCountByToolName := toolByteCountsOf(tools)

	batches := toolSelectionBatches(tools, byteCountByToolName, 4)

	for _, batch := range batches {
		providerIDs := map[string]bool{}
		for _, tool := range batch {
			providerIDs[tool.providerID] = true
		}
		for providerID := range providerIDs {
			placedToolNames := providerOfBatches(batches, providerID)
			if len(placedToolNames) != 10 {
				t.Fatalf("expected provider %s to keep its 10 tools, got %d", providerID, len(placedToolNames))
			}
			batchToolCount := 0
			for _, tool := range batch {
				if tool.providerID == providerID {
					batchToolCount++
				}
			}
			if batchToolCount != 10 {
				t.Fatalf("expected provider %s to stay whole, got %d of its tools in one batch", providerID, batchToolCount)
			}
		}
	}
}

func TestAProviderLargerThanABatchIsSplitIntoContiguousNameOrderedRuns(t *testing.T) {
	syntheticTools := syntheticCatalog(1, 60)
	for toolIndex := range 2 {
		syntheticTools = append(syntheticTools, syntheticTool{
			name:        "small_provider_tool_" + strconv.Itoa(toolIndex),
			providerID:  "small_provider",
			description: "a small provider's tool",
		})
	}
	tools := describedSyntheticTools(syntheticTools)
	byteCountByToolName := toolByteCountsOf(tools)

	batches := toolSelectionBatches(tools, byteCountByToolName, 4)

	oversizedProviderToolNames := []string{}
	for _, batch := range batches {
		runToolNames := []string{}
		for _, tool := range batch {
			if tool.providerID == "provider_0" {
				runToolNames = append(runToolNames, tool.Name)
			}
		}
		if len(runToolNames) == 0 {
			continue
		}
		if !sort.StringsAreSorted(runToolNames) {
			t.Fatalf("expected a run in name order, got %v", runToolNames)
		}
		oversizedProviderToolNames = append(oversizedProviderToolNames, runToolNames...)
	}
	if !sort.StringsAreSorted(oversizedProviderToolNames) {
		t.Fatalf("expected the runs to be contiguous in name order, got %v", oversizedProviderToolNames)
	}
	if len(oversizedProviderToolNames) != 60 {
		t.Fatalf("expected every tool of the split provider to survive, got %d", len(oversizedProviderToolNames))
	}
}

func TestBatchesDoNotDependOnTheOrderProvidersWereRegistered(t *testing.T) {
	syntheticTools := syntheticCatalog(8, 12)
	reversedTools := append([]syntheticTool{}, syntheticTools...)
	for leftIndex, rightIndex := 0, len(reversedTools)-1; leftIndex < rightIndex; leftIndex, rightIndex = leftIndex+1, rightIndex-1 {
		reversedTools[leftIndex], reversedTools[rightIndex] = reversedTools[rightIndex], reversedTools[leftIndex]
	}

	firstBatches := toolSelectionBatches(describedSyntheticTools(syntheticTools), toolByteCountsOf(describedSyntheticTools(syntheticTools)), 5)
	secondBatches := toolSelectionBatches(describedSyntheticTools(reversedTools), toolByteCountsOf(describedSyntheticTools(reversedTools)), 5)

	if batchDocument(t, firstBatches) != batchDocument(t, secondBatches) {
		t.Fatalf("expected the same batches whatever the registration order, got\n%s\nand\n%s", batchDocument(t, firstBatches), batchDocument(t, secondBatches))
	}
}

func batchDocument(t *testing.T, batches [][]decisionTool) string {
	t.Helper()
	batchToolNames := [][]string{}
	for _, batch := range batches {
		batchToolNames = append(batchToolNames, toolNamesOf(batch))
	}
	document, errorValue := json.Marshal(batchToolNames)
	if errorValue != nil {
		t.Fatalf("expected the batches to serialize: %v", errorValue)
	}
	return string(document)
}

func TestNoToolIsAskedTwiceAndNoneIsDropped(t *testing.T) {
	tools := describedSyntheticTools(syntheticCatalog(9, 17))
	byteCountByToolName := toolByteCountsOf(tools)

	batches := toolSelectionBatches(tools, byteCountByToolName, 6)

	placedToolNames := map[string]int{}
	for _, batch := range batches {
		for _, tool := range batch {
			placedToolNames[tool.Name]++
		}
	}
	if len(placedToolNames) != len(tools) {
		t.Fatalf("expected every one of %d tools to be asked about, got %d", len(tools), len(placedToolNames))
	}
	for toolName, placementCount := range placedToolNames {
		if placementCount != 1 {
			t.Fatalf("expected %s to be asked once, got %d times", toolName, placementCount)
		}
	}
}
