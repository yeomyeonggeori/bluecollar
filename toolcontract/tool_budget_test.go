package toolcontract

import (
	"context"
	"testing"
	"time"
)

func toolSetWithBudget(timeoutMS int, handler func(context.Context, ToolInvocation) (ToolResult, error)) *ToolSet {
	toolSet := NewToolSet([]string{"slow_tool"})
	if errorValue := toolSet.RegisterTool(ToolDescriptor{
		Name:           "slow_tool",
		Description:    "a tool with its own budget",
		Visibility:     ToolVisibilityModel,
		ResultContract: &ToolResultContract{Schema: []byte(`{"type":"object"}`)},
		TimeoutMS:      timeoutMS,
	}, handler); errorValue != nil {
		panic(errorValue)
	}
	return toolSet
}

func TestAToolThatOverrunsItsOwnBudgetFailsWithTheBudgetNamed(t *testing.T) {
	toolSet := toolSetWithBudget(20, func(ctx context.Context, _ ToolInvocation) (ToolResult, error) {
		<-ctx.Done()
		return ToolResult{}, ctx.Err()
	})

	result, _ := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "slow_tool"})

	if !result.Failed() {
		t.Fatal("a call that ran past the budget its own descriptor declared did not finish, so it is not a success")
	}
	if result.Failure.Code != FailureCodes.Timeout.String() {
		t.Fatalf("an agent deciding whether to retry needs to know it ran out of time, got %q", result.Failure.Code)
	}
	if !result.Failure.Retryable {
		t.Fatal("a slow call is the kind of failure that is worth trying again")
	}
}

func TestAToolThatFinishesInsideItsBudgetIsUntouched(t *testing.T) {
	toolSet := toolSetWithBudget(2000, func(_ context.Context, _ ToolInvocation) (ToolResult, error) {
		return ToolResult{Output: ToolOutput{Content: "done", Data: []byte(`{}`)}}, nil
	})

	result, errorValue := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "slow_tool"})

	if errorValue != nil || result.Failed() {
		t.Fatalf("a tool inside its budget must return exactly what it produced, got %+v", result.Failure)
	}
	if result.Output.Content != "done" {
		t.Fatalf("the budget must not touch the result, got %q", result.Output.Content)
	}
}

func TestAToolDeclaringNoBudgetIsNeverDeadlined(t *testing.T) {
	toolSet := toolSetWithBudget(0, func(ctx context.Context, _ ToolInvocation) (ToolResult, error) {
		select {
		case <-ctx.Done():
			return ToolResult{}, ctx.Err()
		case <-time.After(30 * time.Millisecond):
			return ToolResult{Output: ToolOutput{Content: "slow but allowed", Data: []byte(`{}`)}}, nil
		}
	})

	result, _ := toolSet.Invoke(context.Background(), ToolInvocation{ToolName: "slow_tool"})

	if result.Failed() {
		t.Fatalf("a tool that declared no budget has none, got %+v", result.Failure)
	}
}

func TestTheCallersOwnCancellationIsNotReportedAsABudgetOverrun(t *testing.T) {
	toolSet := toolSetWithBudget(5000, func(ctx context.Context, _ ToolInvocation) (ToolResult, error) {
		<-ctx.Done()
		return ToolResult{}, ctx.Err()
	})
	callerContext, cancelCaller := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancelCaller()
	}()

	result, _ := toolSet.Invoke(callerContext, ToolInvocation{ToolName: "slow_tool"})

	if result.Failed() && result.Failure.Code == FailureCodes.Timeout.String() {
		t.Fatal("the caller walking away is not the tool being slow, and telling the agent otherwise sends it to retry a task nobody is waiting for")
	}
}
