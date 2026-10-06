package turnclassification

import (
	"github.com/yeomyeonggeori/blueprotocol/agentcontract"
)

var orderedTaskLevels = []agentcontract.TaskLevel{agentcontract.TaskLevelXLow, agentcontract.TaskLevelLow, agentcontract.TaskLevelMedium, agentcontract.TaskLevelHigh, agentcontract.TaskLevelXHigh, agentcontract.TaskLevelMax}

func TaskLevelRank(taskLevel agentcontract.TaskLevel) int {
	normalizedTaskLevel := agentcontract.NormalizeTaskLevel(string(taskLevel))
	for index, orderedTaskLevel := range orderedTaskLevels {
		if orderedTaskLevel == normalizedTaskLevel {
			return index
		}
	}
	return -1
}

func LargerTaskLevel(first agentcontract.TaskLevel, second agentcontract.TaskLevel) agentcontract.TaskLevel {
	if TaskLevelRank(second) > TaskLevelRank(first) {
		return second
	}
	return first
}
