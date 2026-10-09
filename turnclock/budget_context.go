package turnclock

import (
	"context"
	"sync"
	"time"
)

type budgetContext struct {
	context.Context
	remaining func() time.Duration
	done      chan struct{}
	mutex     sync.Mutex
	err       error
}

func newBudgetContext(parent context.Context, remaining func() time.Duration) *budgetContext {
	return &budgetContext{Context: parent, remaining: remaining, done: make(chan struct{})}
}

func (budget *budgetContext) Deadline() (time.Time, bool) {
	projectedDeadline := time.Now().Add(budget.remaining())
	parentDeadline, hasParentDeadline := budget.Context.Deadline()
	if hasParentDeadline && parentDeadline.Before(projectedDeadline) {
		return parentDeadline, true
	}
	return projectedDeadline, true
}

func (budget *budgetContext) Done() <-chan struct{} {
	return budget.done
}

func (budget *budgetContext) Err() error {
	budget.mutex.Lock()
	defer budget.mutex.Unlock()
	return budget.err
}

func (budget *budgetContext) finish(err error) {
	budget.mutex.Lock()
	defer budget.mutex.Unlock()
	if budget.err != nil {
		return
	}
	budget.err = err
	close(budget.done)
}
