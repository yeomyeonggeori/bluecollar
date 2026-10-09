package turnclock

import (
	"context"
	"time"
)

type clockKey struct{}

func With(ctx context.Context, clock *Clock) context.Context {
	return context.WithValue(ctx, clockKey{}, clock)
}

func From(ctx context.Context) (*Clock, bool) {
	clock, isFound := ctx.Value(clockKey{}).(*Clock)
	return clock, isFound
}

func Pause(ctx context.Context) (resume func()) {
	clock, isFound := From(ctx)
	if !isFound {
		return func() {}
	}
	return clock.Pause()
}

func ActiveSince(ctx context.Context, startedAt time.Time) time.Duration {
	clock, isFound := From(ctx)
	if !isFound {
		return time.Since(startedAt)
	}
	return clock.ActiveSince(startedAt)
}

func WithActiveBudget(ctx context.Context, startedAt time.Time, budget time.Duration) (context.Context, context.CancelFunc) {
	clock, isFound := From(ctx)
	if !isFound {
		return context.WithDeadline(ctx, startedAt.Add(budget))
	}
	return clock.WithActiveBudget(ctx, startedAt, budget)
}
