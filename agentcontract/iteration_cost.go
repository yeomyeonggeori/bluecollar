package agentcontract

import (
	"sort"
	"sync"
	"time"
)

type IterationCost struct {
	CostPerIteration time.Duration
	SampleCount      int
}

func unmeasuredCostPerIteration() time.Duration {
	generating := time.Duration(float64(measuredOutputTokensPerModelCall) / unmeasuredOutputTokensPerSecond * float64(time.Second))
	return localCostPerModelCall + generating
}

func DurationForIterationCount(iterationCount int, iterationCost IterationCost, ceiling time.Duration) time.Duration {
	costPerIteration := iterationCost.CostPerIteration
	if costPerIteration <= 0 {
		costPerIteration = unmeasuredCostPerIteration()
	}
	measured := time.Duration(iterationCount) * costPerIteration * durationMargin
	shortest := time.Duration(iterationCount) * fastestPlausibleCostPerCall * durationMargin
	return min(max(measured, shortest), ceiling)
}

type IterationCostObserver struct {
	mutex           sync.Mutex
	costsByModel    map[string][]time.Duration
	lastRecordedFor string
}

const iterationCostSampleCeiling = 100

func NewIterationCostObserver() *IterationCostObserver {
	return &IterationCostObserver{costsByModel: map[string][]time.Duration{}}
}

func (observer *IterationCostObserver) Record(modelName string, iterationCost time.Duration) {
	if observer == nil || modelName == "" || iterationCost <= 0 {
		return
	}
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	costs := append(observer.costsByModel[modelName], iterationCost)
	if len(costs) > iterationCostSampleCeiling {
		costs = costs[len(costs)-iterationCostSampleCeiling:]
	}
	observer.costsByModel[modelName] = costs
	observer.lastRecordedFor = modelName
}

func (observer *IterationCostObserver) CostOfModelInUse() IterationCost {
	if observer == nil {
		return IterationCost{}
	}
	observer.mutex.Lock()
	modelName := observer.lastRecordedFor
	observer.mutex.Unlock()
	return observer.CostForModel(modelName)
}

func (observer *IterationCostObserver) CostForModel(modelName string) IterationCost {
	if observer == nil {
		return IterationCost{}
	}
	observer.mutex.Lock()
	defer observer.mutex.Unlock()
	costs := observer.costsByModel[modelName]
	if len(costs) == 0 {
		return IterationCost{}
	}
	return IterationCost{CostPerIteration: medianDuration(costs), SampleCount: len(costs)}
}

func medianDuration(values []time.Duration) time.Duration {
	ordered := append([]time.Duration{}, values...)
	sort.Slice(ordered, func(left int, right int) bool { return ordered[left] < ordered[right] })
	middle := len(ordered) / 2
	if len(ordered)%2 == 1 {
		return ordered[middle]
	}
	return (ordered[middle-1] + ordered[middle]) / 2
}
