package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/loop"
	"github.com/yeomyeonggeori/bluecollar/model/openaicompatible"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

type clockInput struct {
	TimeZone string `json:"timeZone"`
}

type clockOutput struct {
	Time string `json:"time"`
}

func main() {
	ctx := context.Background()

	kernel := loop.NewAgentKernel(taskstate.NewTaskRunService(taskstate.NewTaskEventService()), taskstate.NewTaskStepService())
	kernel.UseLanguageModelProvider(openaicompatible.NewProvider("http://127.0.0.1:11434/v1", "", "qwen3.5:4b"))

	tools := toolcontract.NewToolSet(nil)
	toolcontract.RegisterToolFunction(tools, toolcontract.ToolFunction[clockInput, clockOutput]{
		Definition: toolcontract.ToolDefinition{
			Name:            "time_get",
			Description:     "Get the current date and time in one time zone.",
			Visibility:      toolcontract.ToolVisibilityModel,
			SideEffectClass: toolcontract.ToolSideEffectRead,
			InputSchema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["timeZone"],
				"properties":{"timeZone":{"type":"string","description":"an IANA time zone, such as Europe/Paris"}}}`),
			ResultContract: &toolcontract.ToolResultContract{Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["time"],
				"properties":{"time":{"type":"string"}}}`)},
		},
		Handler: func(_ context.Context, input clockInput) (clockOutput, error) {
			location, errorValue := time.LoadLocation(input.TimeZone)
			if errorValue != nil {
				return clockOutput{}, errorValue
			}
			return clockOutput{Time: time.Now().In(location).Format("Monday 2 January 2006, 15:04")}, nil
		},
	})

	startTask := agentcontract.TurnDecision{
		Route:             agentcontract.TurnRouteStartTask,
		Classification:    agentcontract.IntakeClassificationBoundedTask,
		TaskShape:         agentcontract.TaskShapeMaintenanceTask,
		TaskLevel:         agentcontract.TaskLevelLow,
		InitialToolNames:  []string{"time_get"},
		ExpectedToolCount: agentcontract.ExpectedToolCountOne,
	}
	result, errorValue := kernel.RunTurn(ctx, agentcontract.AgentTurnRequest{
		RequesterPersonID:       "person-1",
		RequesterName:           "Alex",
		ConversationID:          "conversation-1",
		Prompt:                  "What time is it in Paris right now?",
		ToolSet:                 tools,
		PrecomputedTurnDecision: &startTask,
	})
	if errorValue != nil {
		log.Fatal(errorValue)
	}
	fmt.Println(result.FinishMessage)
}
