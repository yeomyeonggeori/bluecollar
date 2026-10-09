package turnclock

import (
	"context"
	"sync"
	"time"
)

type Clock struct {
	mutex      sync.Mutex
	pauses     []pause
	openPauses int
	alarms     map[*alarm]struct{}
}

type pause struct {
	startedAt time.Time
	endedAt   time.Time
}

type alarm struct {
	startedAt time.Time
	budget    time.Duration
	timer     *time.Timer
	context   *budgetContext
}

func New() *Clock {
	return &Clock{alarms: map[*alarm]struct{}{}}
}

func (clock *Clock) ActiveSince(startedAt time.Time) time.Duration {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.activeBetween(startedAt, time.Now())
}

func (clock *Clock) Pause() (resume func()) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	if clock.openPauses == 0 {
		clock.pauses = append(clock.pauses, pause{startedAt: time.Now()})
		clock.silenceAlarms()
	}
	clock.openPauses++
	var resumeOnce sync.Once
	return func() { resumeOnce.Do(clock.resume) }
}

func (clock *Clock) WithActiveBudget(parent context.Context, startedAt time.Time, budget time.Duration) (context.Context, context.CancelFunc) {
	pendingAlarm := &alarm{startedAt: startedAt, budget: budget}
	budgetContext := newBudgetContext(parent, func() time.Duration { return clock.remaining(pendingAlarm) })
	pendingAlarm.context = budgetContext
	if clock.remaining(pendingAlarm) <= 0 && !clock.isPaused() {
		budgetContext.finish(context.DeadlineExceeded)
		return budgetContext, func() {}
	}
	stopWatchingParent := context.AfterFunc(parent, func() {
		clock.disarm(pendingAlarm)
		budgetContext.finish(parent.Err())
	})
	clock.mutex.Lock()
	clock.alarms[pendingAlarm] = struct{}{}
	clock.arm(pendingAlarm)
	clock.mutex.Unlock()
	return budgetContext, func() {
		stopWatchingParent()
		clock.disarm(pendingAlarm)
		budgetContext.finish(context.Canceled)
	}
}

func (clock *Clock) remaining(pendingAlarm *alarm) time.Duration {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return max(pendingAlarm.budget-clock.activeBetween(pendingAlarm.startedAt, time.Now()), 0)
}

func (clock *Clock) isPaused() bool {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.openPauses > 0
}

func (clock *Clock) resume() {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	clock.openPauses--
	if clock.openPauses > 0 {
		return
	}
	clock.pauses[len(clock.pauses)-1].endedAt = time.Now()
	for pendingAlarm := range clock.alarms {
		clock.arm(pendingAlarm)
	}
}

func (clock *Clock) activeBetween(startedAt time.Time, now time.Time) time.Duration {
	active := now.Sub(startedAt)
	for _, stopped := range clock.pauses {
		active -= stopped.overlapWith(startedAt, now)
	}
	return max(active, 0)
}

func (stopped pause) overlapWith(startedAt time.Time, now time.Time) time.Duration {
	endedAt := stopped.endedAt
	if endedAt.IsZero() {
		endedAt = now
	}
	overlapStart := maxTime(stopped.startedAt, startedAt)
	overlapEnd := minTime(endedAt, now)
	return max(overlapEnd.Sub(overlapStart), 0)
}

func (clock *Clock) arm(pendingAlarm *alarm) {
	if clock.openPauses > 0 {
		return
	}
	remaining := pendingAlarm.budget - clock.activeBetween(pendingAlarm.startedAt, time.Now())
	pendingAlarm.timer = time.AfterFunc(max(remaining, 0), func() { clock.ring(pendingAlarm) })
}

func (clock *Clock) ring(pendingAlarm *alarm) {
	clock.mutex.Lock()
	if _, isPending := clock.alarms[pendingAlarm]; !isPending || clock.openPauses > 0 {
		clock.mutex.Unlock()
		return
	}
	if pendingAlarm.budget > clock.activeBetween(pendingAlarm.startedAt, time.Now()) {
		clock.arm(pendingAlarm)
		clock.mutex.Unlock()
		return
	}
	delete(clock.alarms, pendingAlarm)
	clock.mutex.Unlock()
	pendingAlarm.context.finish(context.DeadlineExceeded)
}

func (clock *Clock) silenceAlarms() {
	for pendingAlarm := range clock.alarms {
		if pendingAlarm.timer != nil {
			pendingAlarm.timer.Stop()
		}
	}
}

func (clock *Clock) disarm(pendingAlarm *alarm) {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	delete(clock.alarms, pendingAlarm)
	if pendingAlarm.timer != nil {
		pendingAlarm.timer.Stop()
	}
}

func maxTime(first time.Time, second time.Time) time.Time {
	if first.After(second) {
		return first
	}
	return second
}

func minTime(first time.Time, second time.Time) time.Time {
	if first.Before(second) {
		return first
	}
	return second
}
