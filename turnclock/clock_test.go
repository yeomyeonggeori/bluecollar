package turnclock

import (
	"context"
	"errors"
	"testing"
	"time"
)

const budget = 80 * time.Millisecond

func TestABudgetEndsWithADeadlineOnceItsActiveTimeIsSpent(t *testing.T) {
	clock := New()
	ctx, cancel := clock.WithActiveBudget(context.Background(), time.Now(), budget)
	defer cancel()

	<-ctx.Done()

	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("a spent budget ended with %v, expected a deadline so callers keep telling it from a cancellation", ctx.Err())
	}
}

func TestAPausedClockDoesNotSpendTheBudget(t *testing.T) {
	clock := New()
	ctx, cancel := clock.WithActiveBudget(context.Background(), time.Now(), budget)
	defer cancel()

	resume := clock.Pause()
	select {
	case <-ctx.Done():
		t.Fatalf("the budget ended (%v) while the clock was paused, so waiting on a person spent the turn", ctx.Err())
	case <-time.After(3 * budget):
	}
	resume()

	select {
	case <-ctx.Done():
	case <-time.After(3 * budget):
		t.Fatal("the budget never ended after the clock resumed")
	}
}

func TestResumingSpendsOnlyWhatWasLeft(t *testing.T) {
	clock := New()
	startedAt := time.Now()
	ctx, cancel := clock.WithActiveBudget(context.Background(), startedAt, budget)
	defer cancel()

	time.Sleep(budget / 2)
	resume := clock.Pause()
	time.Sleep(2 * budget)
	resume()
	<-ctx.Done()

	if active := clock.ActiveSince(startedAt); active < budget || active > 2*budget {
		t.Fatalf("the budget ended after %v of active time, expected about %v with the pause left out", active, budget)
	}
}

func TestNestedPausesResumeOnlyWhenTheLastOneEnds(t *testing.T) {
	clock := New()
	ctx, cancel := clock.WithActiveBudget(context.Background(), time.Now(), budget)
	defer cancel()

	resumeOuter := clock.Pause()
	resumeInner := clock.Pause()
	resumeInner()
	resumeInner()
	select {
	case <-ctx.Done():
		t.Fatal("the budget ended while an outer pause was still open")
	case <-time.After(3 * budget):
	}
	resumeOuter()
	<-ctx.Done()
}

func TestACancelledParentEndsTheBudgetWithItsOwnError(t *testing.T) {
	clock := New()
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := clock.WithActiveBudget(parent, time.Now(), time.Hour)
	defer cancel()

	resume := clock.Pause()
	defer resume()
	cancelParent()
	<-ctx.Done()

	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("a cancelled caller ended the budget with %v, expected the caller's cancellation even while paused", ctx.Err())
	}
}

func TestAChildOfABudgetEndsWithTheDeadline(t *testing.T) {
	clock := New()
	ctx, cancel := clock.WithActiveBudget(context.Background(), time.Now(), budget)
	defer cancel()
	child, cancelChild := context.WithCancel(ctx)
	defer cancelChild()

	<-child.Done()

	if !errors.Is(child.Err(), context.DeadlineExceeded) {
		t.Fatalf("a call made inside the budget ended with %v, expected the budget's deadline", child.Err())
	}
}

func TestWithoutAClockABudgetIsAPlainDeadline(t *testing.T) {
	ctx, cancel := WithActiveBudget(context.Background(), time.Now(), budget)
	defer cancel()

	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		t.Fatal("a caller that carries no clock got a budget without a deadline")
	}
	Pause(ctx)()
}

func TestTheClockTravelsInTheContext(t *testing.T) {
	clock := New()
	ctx := With(context.Background(), clock)
	budgeted, cancel := WithActiveBudget(ctx, time.Now(), time.Hour)
	defer cancel()

	if found, isFound := From(budgeted); !isFound || found != clock {
		t.Fatal("a context made from a budget lost the clock, so a call inside it could not pause the turn")
	}
}

func TestABudgetAlreadySpentEndsBeforeItIsHandedBack(t *testing.T) {
	ctx, cancel := New().WithActiveBudget(context.Background(), time.Now().Add(-time.Second), budget)
	defer cancel()

	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("a budget spent before it was made reads %v, expected it already over", ctx.Err())
	}
}

func TestADeadlineIsProjectedFromTheActiveTimeLeft(t *testing.T) {
	clock := New()
	ctx, cancel := clock.WithActiveBudget(context.Background(), time.Now(), time.Hour)
	defer cancel()

	resume := clock.Pause()
	time.Sleep(budget)
	deadline, hasDeadline := ctx.Deadline()
	resume()

	if !hasDeadline || time.Until(deadline) < time.Hour-budget/2 {
		t.Fatalf("a paused budget projected %v (has=%v), expected the full hour still ahead so a retry copied from it is bounded and fair", time.Until(deadline), hasDeadline)
	}
}

func TestAProjectedDeadlineNeverOutlivesTheCallers(t *testing.T) {
	parent, cancelParent := context.WithTimeout(context.Background(), time.Minute)
	defer cancelParent()
	ctx, cancel := New().WithActiveBudget(parent, time.Now(), time.Hour)
	defer cancel()

	parentDeadline, _ := parent.Deadline()
	if deadline, _ := ctx.Deadline(); !deadline.Equal(parentDeadline) {
		t.Fatalf("the budget projected %v past a caller that ends at %v", deadline, parentDeadline)
	}
}
