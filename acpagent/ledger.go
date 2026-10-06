package acpagent

import (
	"context"
	"encoding/json"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/yeomyeonggeori/bluecollar/acpupdate"
	"github.com/yeomyeonggeori/bluecollar/agentcontract"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

type sessionUpdateSender interface {
	SessionUpdate(context.Context, acp.SessionNotification) error
}

type permissionRequester interface {
	RequestPermission(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

type deferredPermissionRequester struct {
	ready     chan struct{}
	requester permissionRequester
}

func (requester *deferredPermissionRequester) connect(connection permissionRequester) {
	requester.requester = connection
	close(requester.ready)
}

func (requester *deferredPermissionRequester) RequestPermission(ctx context.Context, request acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	select {
	case <-requester.ready:
		return requester.requester.RequestPermission(ctx, request)
	case <-ctx.Done():
		return acp.RequestPermissionResponse{}, ctx.Err()
	}
}

type deferredSessionUpdateSender struct {
	ready  chan struct{}
	sender sessionUpdateSender
}

func (sender *deferredSessionUpdateSender) connect(connection sessionUpdateSender) {
	sender.sender = connection
	close(sender.ready)
}

func (sender *deferredSessionUpdateSender) SessionUpdate(ctx context.Context, notification acp.SessionNotification) error {
	select {
	case <-sender.ready:
		return sender.sender.SessionUpdate(ctx, notification)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func sendLedgerEvent(ctx context.Context, sender sessionUpdateSender, sessionID acp.SessionId, rawTurnEvent taskstate.RawTurnEvent) {
	if sender == nil {
		return
	}
	sender.SessionUpdate(ctx, acp.SessionNotification{
		SessionId: sessionID,
		Update:    acpupdate.ForEvent(rawTurnEvent),
	})
}

func replayLedger(openSession *session, promptMeta map[string]any) bool {
	records, isPresent := ledgerRecordsOfMeta(promptMeta)
	if !isPresent || len(records) == 0 {
		return false
	}
	taskRun := openSession.openTaskRun(promptMeta, "")
	openSession.taskRuns.AdvanceTaskRun(taskRun.TaskRunID, "")
	for _, record := range records {
		openSession.taskRuns.AppendTaskEvent(taskRun.TaskRunID, record.Name, record.EventBody())
	}
	openSession.rememberTaskRun(taskRun.TaskRunID)
	return true
}

func ledgerRecordsOfMeta(promptMeta map[string]any) ([]agentcontract.LedgerRecord, bool) {
	value, isPresent := promptMeta[agentcontract.LedgerMetaKey]
	if !isPresent {
		return nil, false
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return nil, false
	}
	records := []agentcontract.LedgerRecord{}
	if json.Unmarshal(encoded, &records) != nil {
		return nil, false
	}
	return records, true
}

func carriedOutCallsOfMeta(promptMeta map[string]any) []agentcontract.CarriedOutCall {
	value, isPresent := promptMeta[agentcontract.CarriedOutCallMetaKey]
	if !isPresent {
		return nil
	}
	encoded, errorValue := json.Marshal(value)
	if errorValue != nil {
		return nil
	}
	carriedOutCalls := []agentcontract.CarriedOutCall{}
	if json.Unmarshal(encoded, &carriedOutCalls) != nil {
		return nil
	}
	return carriedOutCalls
}

func taskRunIDOfMeta(promptMeta map[string]any) string {
	taskRunID, _ := promptMeta[TaskRunMetaKey].(string)
	return strings.TrimSpace(taskRunID)
}

func (openSession *session) openTaskRun(promptMeta map[string]any, prompt string) agentcontract.TaskRun {
	taskRunID := taskRunIDOfMeta(promptMeta)
	if taskRunID == "" {
		return openSession.taskRuns.CreateTaskRunWithOrigin(requesterPersonID, taskstate.TaskRunOrigin{}, prompt)
	}
	if existingTaskRun, isFound := openSession.taskRuns.FindTaskRun(taskRunID); isFound {
		return existingTaskRun
	}
	taskRun, _ := openSession.taskRuns.CreateTaskRunWithID(taskRunID, requesterPersonID, taskstate.TaskRunOrigin{}, prompt)
	return taskRun
}

func (openSession *session) adoptNamedTaskRun(promptMeta map[string]any, prompt string) {
	if taskRunIDOfMeta(promptMeta) == "" {
		return
	}
	openSession.rememberTaskRun(openSession.openTaskRun(promptMeta, prompt).TaskRunID)
}
