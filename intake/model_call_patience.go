package intake

import (
	"context"
	"errors"
	"time"

	"github.com/yeomyeonggeori/bluecollar/iterationcost"
)

type modelCallCost struct {
	observer *iterationcost.IterationCostObserver
}

func newModelCallCost() modelCallCost {
	return modelCallCost{observer: iterationcost.NewIterationCostObserver()}
}

func (cost modelCallCost) record(modelName string, latency time.Duration) {
	cost.observer.Record(modelName, latency)
}

func askPatiently[Answer any](ctx context.Context, cost modelCallCost, ask func(context.Context) (Answer, error)) (Answer, bool, error) {
	patience, isMeasured := iterationcost.ModelCallPatience(cost.observer.CostOfModelInUse())
	if !isMeasured {
		answer, errorValue := ask(ctx)
		return answer, false, errorValue
	}
	callContext, cancelCall := context.WithTimeout(ctx, patience)
	answer, errorValue := ask(callContext)
	wasCut := errors.Is(callContext.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	cancelCall()
	if errorValue == nil || !wasCut {
		return answer, false, errorValue
	}
	answer, errorValue = ask(ctx)
	return answer, true, errorValue
}
