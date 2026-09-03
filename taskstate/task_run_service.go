package taskstate

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yeomyeonggeori/bluecollar/agentcontract"
)

type TaskRunRepository interface {
	SaveTaskRun(agentcontract.TaskRun) error
	StartTaskRunAttempt(agentcontract.TaskRun, agentcontract.TaskAttempt) error
	FinishTaskRunAttempt(agentcontract.TaskRun, agentcontract.TaskAttempt) error
	TransitionTaskRun(agentcontract.TaskRunTransition) (agentcontract.TaskRun, error)
	FindTaskRun(string) (agentcontract.TaskRun, bool, error)
	FindTaskAttempt(string) (agentcontract.TaskAttempt, bool, error)
	ListTaskRun() ([]agentcontract.TaskRun, error)
	ListTaskRunByPersonID(string) ([]agentcontract.TaskRun, error)
	DeleteTaskRun(string, []string) (bool, error)
	DeleteTaskRunsBefore(time.Time, []string) ([]string, error)
}

var (
	ErrTaskRunNotFound     = errors.New("task run not found")
	ErrTaskRunAccessDenied = errors.New("task run access denied")
	ErrTaskRunNotDeletable = errors.New("task run is not deletable")
)

type ErrIllegalTransition struct {
	TaskRunID     string
	CurrentStatus agentcontract.TaskStatus
	FromStates    []agentcontract.TaskStatus
	ToState       agentcontract.TaskStatus
}

func (transitionError ErrIllegalTransition) Error() string {
	return "illegal task run transition from " + string(transitionError.CurrentStatus) + " to " + string(transitionError.ToState) + " for task run " + transitionError.TaskRunID
}

type TaskRunService struct {
	mutex                    sync.RWMutex
	taskRuns                 map[string]agentcontract.TaskRun
	taskAttempts             map[string]agentcontract.TaskAttempt
	activeAttempts           map[string]activeTaskAttempt
	taskEventService         *TaskEventService
	repository               TaskRunRepository
	runnerID                 string
	transitionObserverMutex  sync.RWMutex
	transitionObservers      map[int]func(agentcontract.TaskRun)
	nextTransitionObserverID int
}

type activeTaskAttempt struct {
	TaskRunID                string
	CancelFunction           context.CancelFunc
	CurrentToolName          string
	CurrentToolObservationID string
}

type InterruptedTaskResumeSelection struct {
	SelectedTaskRuns []agentcontract.TaskRun
	SkippedTaskRuns  []agentcontract.TaskRun
}

func NewTaskRunService(taskEventService *TaskEventService) *TaskRunService {
	return &TaskRunService{
		taskRuns:            map[string]agentcontract.TaskRun{},
		taskAttempts:        map[string]agentcontract.TaskAttempt{},
		activeAttempts:      map[string]activeTaskAttempt{},
		taskEventService:    taskEventService,
		runnerID:            defaultTaskRunnerID(),
		transitionObservers: map[int]func(agentcontract.TaskRun){},
	}
}

func (taskRunService *TaskRunService) UseRepository(repository TaskRunRepository) {
	taskRunService.repository = repository
}

func (taskRunService *TaskRunService) CreateTaskRun(requesterPersonID string, originConversationID string, prompt string) agentcontract.TaskRun {
	return taskRunService.CreateTaskRunWithOrigin(requesterPersonID, TaskRunOrigin{ConversationID: originConversationID}, prompt)
}

func (taskRunService *TaskRunService) CreateTaskRunWithOrigin(requesterPersonID string, origin TaskRunOrigin, prompt string) agentcontract.TaskRun {
	taskRun, _ := taskRunService.CreateTaskRunWithOriginAndError(requesterPersonID, origin, prompt)
	return taskRun
}

func (taskRunService *TaskRunService) CreateTaskRunWithOriginAndError(requesterPersonID string, origin TaskRunOrigin, prompt string) (agentcontract.TaskRun, error) {
	taskRun := agentcontract.TaskRun{
		TaskRunID:            NewIdentifier(),
		RequesterPersonID:    requesterPersonID,
		OriginConversationID: strings.TrimSpace(origin.ConversationID),
		OriginReplyTargetID:  strings.TrimSpace(origin.ReplyTargetID),
		OriginIsThread:       origin.IsThread,
		Status:               agentcontract.TaskStatusPlanned,
		Prompt:               prompt,
		CreatedAt:            time.Now(),
		UpdatedAt:            time.Now(),
	}

	taskRunService.mutex.Lock()
	taskRunService.taskRuns[taskRun.TaskRunID] = taskRun
	taskRunService.mutex.Unlock()
	if errorValue := taskRunService.saveTaskRun(taskRun); errorValue != nil {
		return taskRun, errorValue
	}

	if _, errorValue := taskRunService.taskEventService.AppendTaskEventWithError(taskRun.TaskRunID, agentcontract.TaskEventTaskCreated, prompt); errorValue != nil {
		return taskRun, errorValue
	}
	return taskRun, nil
}

func (taskRunService *TaskRunService) AppendTaskEvent(taskRunID string, name string, body string) {
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, name, body)
}

func (taskRunService *TaskRunService) AppendTaskEventWithError(taskRunID string, name string, body string) (agentcontract.TaskEvent, error) {
	return taskRunService.taskEventService.AppendTaskEventWithError(taskRunID, name, body)
}

func (taskRunService *TaskRunService) RegisterTaskRunObserver(taskRunID string, observer func(RawTurnEvent)) func() {
	return taskRunService.taskEventService.RegisterTaskRunObserver(taskRunID, observer)
}

func (taskRunService *TaskRunService) RegisterTaskRunTransitionObserver(observer func(agentcontract.TaskRun)) func() {
	if observer == nil {
		return func() {}
	}
	taskRunService.transitionObserverMutex.Lock()
	observerID := taskRunService.nextTransitionObserverID
	taskRunService.nextTransitionObserverID++
	taskRunService.transitionObservers[observerID] = observer
	taskRunService.transitionObserverMutex.Unlock()
	return func() {
		taskRunService.transitionObserverMutex.Lock()
		delete(taskRunService.transitionObservers, observerID)
		taskRunService.transitionObserverMutex.Unlock()
	}
}

func (taskRunService *TaskRunService) notifyTaskRunTransitionObservers(taskRun agentcontract.TaskRun) {
	taskRunService.transitionObserverMutex.RLock()
	observers := make([]func(agentcontract.TaskRun), 0, len(taskRunService.transitionObservers))
	for _, observer := range taskRunService.transitionObservers {
		observers = append(observers, observer)
	}
	taskRunService.transitionObserverMutex.RUnlock()
	for _, observer := range observers {
		observer(taskRun)
	}
}

func (taskRunService *TaskRunService) RegisterTaskRunCancel(taskRunID string, cancelFunction context.CancelFunc) func() {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	if trimmedTaskRunID == "" || cancelFunction == nil {
		return func() {}
	}
	taskRunService.mutex.Lock()
	taskRun, isFound := taskRunService.findTaskRunForMutation(trimmedTaskRunID)
	if !isFound || !taskRunService.taskRunHasActiveAttemptLocked(taskRun) {
		taskRunService.mutex.Unlock()
		return func() {}
	}
	taskAttemptID := taskRun.CurrentAttemptID
	activeAttempt := taskRunService.activeAttempts[taskAttemptID]
	activeAttempt.TaskRunID = trimmedTaskRunID
	activeAttempt.CancelFunction = cancelFunction
	taskRunService.activeAttempts[taskAttemptID] = activeAttempt
	taskRunService.mutex.Unlock()
	return func() {
		taskRunService.mutex.Lock()
		activeAttempt, isFound := taskRunService.activeAttempts[taskAttemptID]
		if isFound && activeAttempt.TaskRunID == trimmedTaskRunID {
			activeAttempt.CancelFunction = nil
			taskRunService.activeAttempts[taskAttemptID] = activeAttempt
		}
		taskRunService.mutex.Unlock()
	}
}

func (taskRunService *TaskRunService) RegisterTaskRunTool(taskRunID string, observationID string, toolName string) func() {
	trimmedTaskRunID := strings.TrimSpace(taskRunID)
	trimmedObservationID := strings.TrimSpace(observationID)
	trimmedToolName := strings.TrimSpace(toolName)
	if trimmedTaskRunID == "" || trimmedToolName == "" {
		return func() {}
	}
	taskRunService.mutex.Lock()
	taskRun, isFound := taskRunService.findTaskRunForMutation(trimmedTaskRunID)
	if !isFound || !taskRunService.taskRunHasActiveAttemptLocked(taskRun) {
		taskRunService.mutex.Unlock()
		return func() {}
	}
	taskAttemptID := taskRun.CurrentAttemptID
	activeAttempt := taskRunService.activeAttempts[taskAttemptID]
	activeAttempt.TaskRunID = trimmedTaskRunID
	activeAttempt.CurrentToolName = trimmedToolName
	activeAttempt.CurrentToolObservationID = trimmedObservationID
	taskRunService.activeAttempts[taskAttemptID] = activeAttempt
	taskRunService.mutex.Unlock()
	return func() {
		taskRunService.mutex.Lock()
		activeAttempt, isFound := taskRunService.activeAttempts[taskAttemptID]
		if isFound && activeAttempt.TaskRunID == trimmedTaskRunID && activeAttempt.CurrentToolObservationID == trimmedObservationID {
			activeAttempt.CurrentToolName = ""
			activeAttempt.CurrentToolObservationID = ""
			taskRunService.activeAttempts[taskAttemptID] = activeAttempt
		}
		taskRunService.mutex.Unlock()
	}
}

func (taskRunService *TaskRunService) IsTaskRunCancelled(taskRunID string) bool {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	return isFound && taskRun.Status == agentcontract.TaskStatusCancelled
}

func (taskRunService *TaskRunService) ListTaskEvent(taskRunID string) []agentcontract.TaskEvent {
	return taskRunService.taskEventService.ListTaskEvent(taskRunID)
}

func (taskRunService *TaskRunService) AdvanceTaskRun(taskRunID string, currentAgentProfileName string) (agentcontract.TaskRun, error) {
	now := time.Now()
	taskAttempt := agentcontract.TaskAttempt{
		TaskAttemptID: NewIdentifier(),
		TaskRunID:     taskRunID,
		RunnerID:      taskRunService.runnerID,
		Status:        agentcontract.TaskAttemptStatusRunning,
		StartedAt:     now,
	}
	return taskRunService.TransitionTaskRun(agentcontract.TaskRunTransition{
		TaskRunID:               taskRunID,
		FromStates:              advanceTaskRunFromStates(),
		ToState:                 agentcontract.TaskStatusRunning,
		CurrentAgentProfileName: currentAgentProfileName,
		StartedAttempt:          &taskAttempt,
		Event:                   newTaskRunTransitionEvent(taskRunID, agentcontract.TaskStatusRunning, currentAgentProfileName, now),
		UpdatedAt:               now,
	})
}

func (taskRunService *TaskRunService) PauseTaskRun(taskRunID string, status agentcontract.TaskStatus, reason string) (agentcontract.TaskRun, error) {
	now := time.Now()
	return taskRunService.TransitionTaskRun(agentcontract.TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            pauseTaskRunFromStates(),
		ToState:               status,
		FailureReason:         reason,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: taskAttemptStatusForTaskStatus(status),
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, status, reason, now),
		UpdatedAt:             now,
	})
}

func (taskRunService *TaskRunService) FailTaskRun(taskRunID string, reason string) (agentcontract.TaskRun, error) {
	now := time.Now()
	return taskRunService.TransitionTaskRun(agentcontract.TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            failTaskRunFromStates(),
		ToState:               agentcontract.TaskStatusFailed,
		FailureReason:         reason,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: agentcontract.TaskAttemptStatusFailed,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, agentcontract.TaskStatusFailed, reason, now),
		UpdatedAt:             now,
	})
}

func (taskRunService *TaskRunService) TransitionTaskRun(transition agentcontract.TaskRunTransition) (agentcontract.TaskRun, error) {
	taskRun, errorValue := taskRunService.transitionTaskRunExclusively(transition)
	if errorValue != nil {
		return agentcontract.TaskRun{}, errorValue
	}
	taskRunService.notifyTaskRunTransitionObservers(taskRun)
	return taskRun, nil
}

func (taskRunService *TaskRunService) transitionTaskRunExclusively(transition agentcontract.TaskRunTransition) (agentcontract.TaskRun, error) {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()

	normalizedTransition := taskRunService.normalizeTaskRunTransition(transition)
	if taskRunService.repository != nil {
		taskRun, errorValue := taskRunService.repository.TransitionTaskRun(normalizedTransition)
		if errorValue != nil {
			return agentcontract.TaskRun{}, errorValue
		}
		taskRunService.applyTransitionCacheLocked(normalizedTransition, taskRun)
		return taskRun, nil
	}
	return taskRunService.transitionTaskRunInMemoryLocked(normalizedTransition)
}

func (taskRunService *TaskRunService) normalizeTaskRunTransition(transition agentcontract.TaskRunTransition) agentcontract.TaskRunTransition {
	if transition.UpdatedAt.IsZero() {
		transition.UpdatedAt = time.Now()
	}
	if transition.StartedAttempt != nil && transition.StartedAttempt.StartedAt.IsZero() {
		startedAttempt := *transition.StartedAttempt
		startedAttempt.StartedAt = transition.UpdatedAt
		transition.StartedAttempt = &startedAttempt
	}
	if transition.Event != nil && transition.Event.CreatedAt.IsZero() {
		taskEvent := *transition.Event
		taskEvent.CreatedAt = transition.UpdatedAt
		transition.Event = &taskEvent
	}
	if transition.Event != nil && strings.TrimSpace(transition.Event.TaskEventID) == "" {
		taskEvent := *transition.Event
		taskEvent.TaskEventID = NewIdentifier()
		transition.Event = &taskEvent
	}
	return transition
}

func (taskRunService *TaskRunService) transitionTaskRunInMemoryLocked(transition agentcontract.TaskRunTransition) (agentcontract.TaskRun, error) {
	taskRun, isFound := taskRunService.findTaskRunForMutation(transition.TaskRunID)
	if !isFound {
		return agentcontract.TaskRun{}, errors.New("task run not found")
	}
	if !taskStatusAllowed(taskRun.Status, transition.FromStates) {
		return agentcontract.TaskRun{}, ErrIllegalTransition{
			TaskRunID:     transition.TaskRunID,
			CurrentStatus: taskRun.Status,
			FromStates:    append([]agentcontract.TaskStatus{}, transition.FromStates...),
			ToState:       transition.ToState,
		}
	}

	taskRun = applyTaskRunTransition(taskRun, transition)
	taskRunService.taskRuns[transition.TaskRunID] = taskRun
	taskRunService.applyTransitionAttemptCacheLocked(transition, taskRun)
	taskRunService.recordTransitionEvent(transition)
	return taskRun, nil
}

func (taskRunService *TaskRunService) applyTransitionCacheLocked(transition agentcontract.TaskRunTransition, taskRun agentcontract.TaskRun) {
	taskRunService.taskRuns[taskRun.TaskRunID] = taskRun
	taskRunService.applyTransitionAttemptCacheLocked(transition, taskRun)
	taskRunService.recordTransitionEvent(transition)
}

func (taskRunService *TaskRunService) applyTransitionAttemptCacheLocked(transition agentcontract.TaskRunTransition, taskRun agentcontract.TaskRun) {
	if transition.StartedAttempt != nil {
		taskRunService.taskAttempts[transition.StartedAttempt.TaskAttemptID] = *transition.StartedAttempt
		taskRunService.activeAttempts[transition.StartedAttempt.TaskAttemptID] = activeTaskAttempt{TaskRunID: taskRun.TaskRunID}
	}
	if !transition.FinishCurrentAttempt {
		return
	}
	taskAttemptID := strings.TrimSpace(taskRun.CurrentAttemptID)
	if taskAttemptID == "" {
		taskRunService.closeOpenToolRequests(taskRun.TaskRunID, "", "cancelled_by_attempt_end")
		return
	}
	taskAttempt := taskRunService.findTaskAttemptForMutation(taskAttemptID, taskRun.TaskRunID)
	taskAttempt.Status = transition.FinishedAttemptStatus
	taskAttempt.FinishedAt = &transition.UpdatedAt
	taskAttempt.FailureReason = strings.TrimSpace(transition.FailureReason)
	taskRunService.taskAttempts[taskAttemptID] = taskAttempt
	delete(taskRunService.activeAttempts, taskAttemptID)
	taskRunService.closeOpenToolRequests(taskRun.TaskRunID, taskAttemptID, "cancelled_by_attempt_end")
}

func (taskRunService *TaskRunService) recordTransitionEvent(transition agentcontract.TaskRunTransition) {
	if transition.Event == nil {
		return
	}
	taskRunService.taskEventService.RecordTaskEvent(*transition.Event)
}

func applyTaskRunTransition(taskRun agentcontract.TaskRun, transition agentcontract.TaskRunTransition) agentcontract.TaskRun {
	taskRun.Status = transition.ToState
	taskRun.UpdatedAt = transition.UpdatedAt
	if transition.StartedAttempt != nil {
		taskRun.CurrentAttemptID = transition.StartedAttempt.TaskAttemptID
		taskRun.CurrentAgentProfileName = transition.CurrentAgentProfileName
		taskRun.FailureReason = ""
	}
	if transition.Result != "" {
		taskRun.Result = transition.Result
	}
	if transition.FailureReason != "" || transition.FinishCurrentAttempt {
		taskRun.FailureReason = transition.FailureReason
	}
	return taskRun
}

func taskStatusAllowed(status agentcontract.TaskStatus, allowedStatuses []agentcontract.TaskStatus) bool {
	for _, allowedStatus := range allowedStatuses {
		if status == allowedStatus {
			return true
		}
	}
	return false
}

func newTaskRunTransitionEvent(taskRunID string, status agentcontract.TaskStatus, body string, createdAt time.Time) *agentcontract.TaskEvent {
	return &agentcontract.TaskEvent{
		TaskEventID: NewIdentifier(),
		TaskRunID:   taskRunID,
		Name:        taskRunTransitionEventName(status),
		Body:        body,
		CreatedAt:   createdAt,
	}
}

func taskRunTransitionEventName(status agentcontract.TaskStatus) string {
	switch status {
	case agentcontract.TaskStatusRunning:
		return agentcontract.TaskEventTaskRunning
	case agentcontract.TaskStatusBlocked:
		return agentcontract.TaskEventTaskBlocked
	case agentcontract.TaskStatusInterrupted:
		return agentcontract.TaskEventTaskInterrupted
	case agentcontract.TaskStatusCompleted:
		return agentcontract.TaskEventTaskCompleted
	case agentcontract.TaskStatusFailed:
		return agentcontract.TaskEventTaskFailed
	case agentcontract.TaskStatusCancelled:
		return agentcontract.TaskEventTaskCancelled
	default:
		return agentcontract.TaskEventTaskPaused
	}
}

func advanceTaskRunFromStates() []agentcontract.TaskStatus {
	return []agentcontract.TaskStatus{
		agentcontract.TaskStatusPlanned,
		agentcontract.TaskStatusRunning,
		agentcontract.TaskStatusWaitingUserInput,
		agentcontract.TaskStatusWaitingApproval,
		agentcontract.TaskStatusBlocked,
		agentcontract.TaskStatusInterrupted,
	}
}

func pauseTaskRunFromStates() []agentcontract.TaskStatus {
	return []agentcontract.TaskStatus{
		agentcontract.TaskStatusPlanned,
		agentcontract.TaskStatusRunning,
		agentcontract.TaskStatusWaitingUserInput,
		agentcontract.TaskStatusWaitingApproval,
		agentcontract.TaskStatusBlocked,
		agentcontract.TaskStatusInterrupted,
	}
}

func failTaskRunFromStates() []agentcontract.TaskStatus {
	return pauseTaskRunFromStates()
}

func completeTaskRunFromStates() []agentcontract.TaskStatus {
	return []agentcontract.TaskStatus{
		agentcontract.TaskStatusPlanned,
		agentcontract.TaskStatusRunning,
		agentcontract.TaskStatusWaitingUserInput,
		agentcontract.TaskStatusWaitingApproval,
		agentcontract.TaskStatusBlocked,
	}
}

func cancelTaskRunFromStates() []agentcontract.TaskStatus {
	return []agentcontract.TaskStatus{
		agentcontract.TaskStatusPlanned,
		agentcontract.TaskStatusRunning,
		agentcontract.TaskStatusWaitingUserInput,
		agentcontract.TaskStatusWaitingApproval,
		agentcontract.TaskStatusBlocked,
	}
}

func interruptInactiveTaskRunFromStates() []agentcontract.TaskStatus {
	return []agentcontract.TaskStatus{
		agentcontract.TaskStatusPlanned,
		agentcontract.TaskStatusRunning,
	}
}

func (taskRunService *TaskRunService) ResumeTaskRun(taskRunID string) (agentcontract.TaskRun, error) {
	return taskRunService.AdvanceTaskRun(taskRunID, "planner")
}

func (taskRunService *TaskRunService) CancelTaskRun(taskRunID string, requesterPersonID string) (agentcontract.TaskRun, error) {
	return taskRunService.cancelTaskRun(taskRunID, requesterPersonID, requesterPersonID)
}

func (taskRunService *TaskRunService) CancelTaskRunWithReason(taskRunID string, requesterPersonID string, reason string) (agentcontract.TaskRun, error) {
	return taskRunService.cancelTaskRun(taskRunID, requesterPersonID, reason)
}

func (taskRunService *TaskRunService) CancelWaitingTaskRuns(requesterPersonID string, originConversationID string, reason string) []agentcontract.TaskRun {
	cancelledTaskRuns := []agentcontract.TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRunByPersonID(requesterPersonID) {
		if !taskRunIsWaiting(taskRun) {
			continue
		}
		if originConversationID != "" && taskRun.OriginConversationID != originConversationID {
			continue
		}
		cancelledTaskRun, errorValue := taskRunService.cancelTaskRun(taskRun.TaskRunID, requesterPersonID, reason)
		if errorValue != nil {
			continue
		}
		taskRunService.taskEventService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskWaitCancelled, reason)
		cancelledTaskRuns = append(cancelledTaskRuns, cancelledTaskRun)
	}
	return cancelledTaskRuns
}

func (taskRunService *TaskRunService) CancelActiveTaskRuns(request TaskRunCancelRequest) []agentcontract.TaskRun {
	cancelledTaskRuns := []agentcontract.TaskRun{}
	for _, taskRun := range taskRunService.taskRunsForCancelRequest(request) {
		if !taskRunMatchesCancelRequest(taskRun, request) {
			continue
		}
		cancelledTaskRun, errorValue := taskRunService.cancelTaskRun(taskRun.TaskRunID, request.RequesterPersonID, request.Reason)
		if errorValue != nil {
			continue
		}
		cancelledTaskRuns = append(cancelledTaskRuns, cancelledTaskRun)
	}
	return cancelledTaskRuns
}

func (taskRunService *TaskRunService) InterruptOrphanedRuntimeTaskRuns(reason string) []agentcontract.TaskRun {
	interruptedTaskRuns := []agentcontract.TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if !taskRunIsRuntimeOwned(taskRun) {
			continue
		}
		interruptedTaskRun, isInterrupted := taskRunService.InterruptInactiveTaskRun(taskRun.TaskRunID, reason)
		if !isInterrupted {
			continue
		}
		interruptedTaskRuns = append(interruptedTaskRuns, interruptedTaskRun)
	}
	return interruptedTaskRuns
}

func (taskRunService *TaskRunService) InterruptRuntimeTaskRunsForPlannedShutdown() []agentcontract.TaskRun {
	interruptedTaskRuns := []agentcontract.TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if !taskRunIsRuntimeOwned(taskRun) {
			continue
		}
		now := time.Now()
		interruptedTaskRun, errorValue := taskRunService.TransitionTaskRun(agentcontract.TaskRunTransition{
			TaskRunID:             taskRun.TaskRunID,
			FromStates:            interruptInactiveTaskRunFromStates(),
			ToState:               agentcontract.TaskStatusInterrupted,
			FailureReason:         agentcontract.TaskInterruptReasonPlannedShutdown,
			FinishCurrentAttempt:  true,
			FinishedAttemptStatus: agentcontract.TaskAttemptStatusInterrupted,
			RunnerID:              taskRunService.runnerID,
			Event:                 newTaskRunTransitionEvent(taskRun.TaskRunID, agentcontract.TaskStatusInterrupted, agentcontract.TaskInterruptReasonPlannedShutdown, now),
			UpdatedAt:             now,
		})
		if errorValue != nil {
			continue
		}
		interruptedTaskRuns = append(interruptedTaskRuns, interruptedTaskRun)
	}
	return interruptedTaskRuns
}

func (taskRunService *TaskRunService) InterruptInactiveTaskRun(taskRunID string, reason string) (agentcontract.TaskRun, bool) {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return agentcontract.TaskRun{}, false
	}
	if taskRun.Status == agentcontract.TaskStatusRunning && taskRunService.IsTaskRunActuallyRunning(taskRun) {
		return agentcontract.TaskRun{}, false
	}

	now := time.Now()
	interruptedTaskRun, errorValue := taskRunService.TransitionTaskRun(agentcontract.TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            interruptInactiveTaskRunFromStates(),
		ToState:               agentcontract.TaskStatusInterrupted,
		FailureReason:         reason,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: agentcontract.TaskAttemptStatusInterrupted,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, agentcontract.TaskStatusInterrupted, reason, now),
		UpdatedAt:             now,
	})
	if errorValue != nil {
		return agentcontract.TaskRun{}, false
	}
	return interruptedTaskRun, true
}

func (taskRunService *TaskRunService) SelectInterruptedTaskRunsForAutoResume(now time.Time, limit int) InterruptedTaskResumeSelection {
	if now.IsZero() {
		now = time.Now()
	}
	candidates := taskRunService.interruptedTaskRunsEligibleForAutoResume(now)
	if limit <= 0 || len(candidates) <= limit {
		return InterruptedTaskResumeSelection{SelectedTaskRuns: candidates}
	}
	return InterruptedTaskResumeSelection{
		SelectedTaskRuns: candidates[:limit],
		SkippedTaskRuns:  candidates[limit:],
	}
}

func (taskRunService *TaskRunService) ClaimInterruptedTaskRunAutoResume(taskRunID string, reason string) bool {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound || taskRun.Status != agentcontract.TaskStatusInterrupted {
		return false
	}
	attemptCount := taskRunService.autoResumeAttemptCount(taskRun.TaskRunID)
	if attemptCount > 0 && !taskRunWasInterruptedByPlannedShutdown(taskRun) {
		return false
	}
	taskRunService.taskEventService.AppendTaskEvent(taskRun.TaskRunID, agentcontract.TaskEventTaskAutoResumeAttempted, marshalTaskRunServiceEventBody(map[string]any{
		"attemptCount": attemptCount + 1,
		"reason":       strings.TrimSpace(reason),
	}))
	return true
}

func (taskRunService *TaskRunService) MarkInterruptedTaskRunAutoResumeSkipped(taskRunID string, reason string) {
	taskRunService.taskEventService.AppendTaskEvent(taskRunID, agentcontract.TaskEventTaskAutoResumeSkipped, marshalTaskRunServiceEventBody(map[string]string{
		"reason": strings.TrimSpace(reason),
	}))
}

func (taskRunService *TaskRunService) IsTaskRunActuallyRunning(taskRun agentcontract.TaskRun) bool {
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()
	return taskRunService.taskRunHasActiveAttemptLocked(taskRun)
}

func (taskRunService *TaskRunService) CloseOpenToolRequests(taskRunID string, reason string) {
	taskRunService.closeOpenToolRequests(taskRunID, "", reason)
}

func (taskRunService *TaskRunService) closeOpenToolRequests(taskRunID string, taskAttemptID string, reason string) {
	openRequests := openToolRequests(taskRunService.taskEventService.ListTaskEvent(taskRunID))
	for _, openRequest := range openRequests {
		taskRunService.taskEventService.AppendTaskEvent(taskRunID, agentcontract.ToolTaskEventName(openRequest.ToolName, agentcontract.ToolTaskEventCancelledSuffix), marshalTaskRunServiceEventBody(map[string]string{
			"observationID":  openRequest.ObservationID,
			"toolName":       openRequest.ToolName,
			"taskAttemptID":  taskAttemptID,
			"reason":         reason,
			"terminalStatus": "cancelled",
		}))
	}
}

func (taskRunService *TaskRunService) taskRunsForCancelRequest(request TaskRunCancelRequest) []agentcontract.TaskRun {
	if len(request.TaskRunIDs) > 0 {
		taskRuns := []agentcontract.TaskRun{}
		for _, taskRunID := range trimUniqueTaskRunIDs(request.TaskRunIDs) {
			taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
			if isFound {
				taskRuns = append(taskRuns, taskRun)
			}
		}
		return taskRuns
	}
	if strings.TrimSpace(request.RequesterPersonID) != "" {
		return taskRunService.ListTaskRunByPersonID(request.RequesterPersonID)
	}
	return taskRunService.ListTaskRun()
}

func taskRunMatchesCancelRequest(taskRun agentcontract.TaskRun, request TaskRunCancelRequest) bool {
	if !taskRunIsActive(taskRun) {
		return false
	}
	if requesterPersonID := strings.TrimSpace(request.RequesterPersonID); requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID {
		return false
	}
	if request.ScheduleOnly && !strings.HasPrefix(taskRun.OriginConversationID, "schedule:") {
		return false
	}
	if originConversationIDPrefix := strings.TrimSpace(request.OriginConversationIDPrefix); originConversationIDPrefix != "" && !strings.HasPrefix(taskRun.OriginConversationID, originConversationIDPrefix) {
		return false
	}
	if len(request.OriginConversationIDs) > 0 && !containsTrimmedString(request.OriginConversationIDs, taskRun.OriginConversationID) {
		return false
	}
	if request.StaleBefore != nil && !taskRun.UpdatedAt.Before(*request.StaleBefore) {
		return false
	}
	return true
}

func taskRunIsActive(taskRun agentcontract.TaskRun) bool {
	switch taskRun.Status {
	case agentcontract.TaskStatusPlanned, agentcontract.TaskStatusRunning, agentcontract.TaskStatusWaitingApproval, agentcontract.TaskStatusWaitingUserInput, agentcontract.TaskStatusBlocked:
		return true
	default:
		return false
	}
}

func taskRunIsWaiting(taskRun agentcontract.TaskRun) bool {
	return taskRun.Status == agentcontract.TaskStatusWaitingApproval || taskRun.Status == agentcontract.TaskStatusWaitingUserInput
}

func taskRunIsRuntimeOwned(taskRun agentcontract.TaskRun) bool {
	return taskRun.Status == agentcontract.TaskStatusPlanned || taskRun.Status == agentcontract.TaskStatusRunning
}

const staleBlockedTaskRunAge = 24 * time.Hour
const staleWaitingTaskRunAge = 72 * time.Hour

func StaleUnattendedTaskRunReason(taskRun agentcontract.TaskRun, now time.Time) string {
	switch taskRun.Status {
	case agentcontract.TaskStatusBlocked:
		if now.Sub(taskRun.UpdatedAt) > staleBlockedTaskRunAge {
			return "blocked_expired"
		}
	case agentcontract.TaskStatusWaitingApproval, agentcontract.TaskStatusWaitingUserInput:
		if now.Sub(taskRun.UpdatedAt) > staleWaitingTaskRunAge {
			return "waiting_expired"
		}
	case agentcontract.TaskStatusInterrupted:
		if !agentcontract.TaskRunWasInterruptedByRuntimeRestart(taskRun) && now.Sub(taskRun.UpdatedAt) > staleBlockedTaskRunAge {
			return "interrupted_expired"
		}
	}
	return ""
}

func (taskRunService *TaskRunService) SelectStaleUnattendedTaskRuns(now time.Time) []agentcontract.TaskRun {
	if now.IsZero() {
		now = time.Now()
	}
	taskRuns := []agentcontract.TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if StaleUnattendedTaskRunReason(taskRun, now) == "" {
			continue
		}
		taskRuns = append(taskRuns, taskRun)
	}
	sort.SliceStable(taskRuns, func(leftIndex int, rightIndex int) bool {
		return taskRuns[leftIndex].UpdatedAt.Before(taskRuns[rightIndex].UpdatedAt)
	})
	return taskRuns
}

func (taskRunService *TaskRunService) interruptedTaskRunsEligibleForAutoResume(now time.Time) []agentcontract.TaskRun {
	taskRuns := []agentcontract.TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if !taskRunService.canAutoResumeInterruptedTaskRun(taskRun, now) {
			continue
		}
		taskRuns = append(taskRuns, taskRun)
	}
	sort.SliceStable(taskRuns, func(leftIndex int, rightIndex int) bool {
		return taskRuns[leftIndex].UpdatedAt.After(taskRuns[rightIndex].UpdatedAt)
	})
	return taskRuns
}

func (taskRunService *TaskRunService) canAutoResumeInterruptedTaskRun(taskRun agentcontract.TaskRun, now time.Time) bool {
	if taskRun.Status != agentcontract.TaskStatusInterrupted {
		return false
	}
	if now.Sub(taskRun.UpdatedAt) > 24*time.Hour {
		return false
	}
	if taskRunService.autoResumeAttemptCount(taskRun.TaskRunID) > 0 && !taskRunWasInterruptedByPlannedShutdown(taskRun) {
		return false
	}
	return taskRunService.taskRunHasInterruptedMarker(taskRun.TaskRunID)
}

func taskRunWasInterruptedByPlannedShutdown(taskRun agentcontract.TaskRun) bool {
	return taskRun.Status == agentcontract.TaskStatusInterrupted && taskRun.FailureReason == agentcontract.TaskInterruptReasonPlannedShutdown
}

func (taskRunService *TaskRunService) taskRunHasInterruptedMarker(taskRunID string) bool {
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventTaskInterrupted {
			return true
		}
	}
	return false
}

func (taskRunService *TaskRunService) autoResumeAttemptCount(taskRunID string) int {
	count := 0
	for _, taskEvent := range taskRunService.ListTaskEvent(taskRunID) {
		if taskEvent.Name == agentcontract.TaskEventTaskAutoResumeAttempted {
			count++
		}
	}
	return count
}

func trimUniqueTaskRunIDs(taskRunIDs []string) []string {
	seenTaskRunIDs := map[string]bool{}
	trimmedTaskRunIDs := []string{}
	for _, taskRunID := range taskRunIDs {
		trimmedTaskRunID := strings.TrimSpace(taskRunID)
		if trimmedTaskRunID == "" || seenTaskRunIDs[trimmedTaskRunID] {
			continue
		}
		seenTaskRunIDs[trimmedTaskRunID] = true
		trimmedTaskRunIDs = append(trimmedTaskRunIDs, trimmedTaskRunID)
	}
	return trimmedTaskRunIDs
}

func containsTrimmedString(values []string, expectedValue string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == expectedValue {
			return true
		}
	}
	return false
}

func (taskRunService *TaskRunService) CompleteTaskRun(taskRunID string, result string) (agentcontract.TaskRun, error) {
	now := time.Now()
	return taskRunService.TransitionTaskRun(agentcontract.TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            completeTaskRunFromStates(),
		ToState:               agentcontract.TaskStatusCompleted,
		Result:                result,
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: agentcontract.TaskAttemptStatusCompleted,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, agentcontract.TaskStatusCompleted, result, now),
		UpdatedAt:             now,
	})
}

func (taskRunService *TaskRunService) RecordTaskRunResult(taskRunID string, result string) (agentcontract.TaskRun, error) {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return agentcontract.TaskRun{}, ErrTaskRunNotFound
	}
	taskRun.Result = result
	taskRun.UpdatedAt = time.Now()
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	if taskRunService.repository != nil {
		if errorValue := taskRunService.repository.SaveTaskRun(taskRun); errorValue != nil {
			return agentcontract.TaskRun{}, errorValue
		}
	}
	taskRunService.taskRuns[taskRunID] = taskRun
	return taskRun, nil
}

func (taskRunService *TaskRunService) FindTaskRun(taskRunID string) (agentcontract.TaskRun, bool) {
	if taskRunService.repository != nil {
		taskRun, isFound, errorValue := taskRunService.repository.FindTaskRun(taskRunID)
		if errorValue == nil {
			return taskRun, isFound
		}
	}
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()

	taskRun, isFound := taskRunService.taskRuns[taskRunID]
	return taskRun, isFound
}

func (taskRunService *TaskRunService) ListTaskRun() []agentcontract.TaskRun {
	if taskRunService.repository != nil {
		taskRuns, errorValue := taskRunService.repository.ListTaskRun()
		if errorValue == nil {
			return taskRuns
		}
	}
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()

	taskRuns := make([]agentcontract.TaskRun, 0, len(taskRunService.taskRuns))
	for _, taskRun := range taskRunService.taskRuns {
		taskRuns = append(taskRuns, taskRun)
	}

	return taskRuns
}

func (taskRunService *TaskRunService) ListTaskRunByPersonID(personID string) []agentcontract.TaskRun {
	if taskRunService.repository != nil {
		taskRuns, errorValue := taskRunService.repository.ListTaskRunByPersonID(personID)
		if errorValue == nil {
			return taskRuns
		}
	}
	taskRuns := []agentcontract.TaskRun{}
	for _, taskRun := range taskRunService.ListTaskRun() {
		if taskRun.RequesterPersonID == personID {
			taskRuns = append(taskRuns, taskRun)
		}
	}
	return taskRuns
}

func (taskRunService *TaskRunService) DeleteTerminalTaskRun(taskRunID string, requesterPersonID string) (agentcontract.TaskRun, error) {
	taskRun, isFound := taskRunService.FindTaskRun(strings.TrimSpace(taskRunID))
	if !isFound {
		return agentcontract.TaskRun{}, ErrTaskRunNotFound
	}
	if requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID {
		return agentcontract.TaskRun{}, ErrTaskRunAccessDenied
	}
	if !isTerminalTaskRunStatus(taskRun.Status) {
		return agentcontract.TaskRun{}, ErrTaskRunNotDeletable
	}
	if taskRunService.repository != nil {
		wasDeleted, errorValue := taskRunService.repository.DeleteTaskRun(taskRun.TaskRunID, terminalTaskRunStatusStrings())
		if errorValue != nil {
			return agentcontract.TaskRun{}, errorValue
		}
		if !wasDeleted {
			return agentcontract.TaskRun{}, ErrTaskRunNotFound
		}
		taskRunService.evictTaskRunIDs([]string{taskRun.TaskRunID})
		return taskRun, nil
	}
	if !taskRunService.deleteTerminalTaskRunFromMemory(taskRun.TaskRunID) {
		return agentcontract.TaskRun{}, ErrTaskRunNotFound
	}
	return taskRun, nil
}

func (taskRunService *TaskRunService) saveTaskRun(taskRun agentcontract.TaskRun) error {
	if taskRunService.repository == nil {
		return nil
	}
	return taskRunService.repository.SaveTaskRun(taskRun)
}

func (taskRunService *TaskRunService) cancelTaskRun(taskRunID string, requesterPersonID string, reason string) (agentcontract.TaskRun, error) {
	taskRun, isFound := taskRunService.FindTaskRun(taskRunID)
	if !isFound {
		return agentcontract.TaskRun{}, errors.New("task run not found")
	}
	if requesterPersonID != "" && taskRun.RequesterPersonID != requesterPersonID {
		return agentcontract.TaskRun{}, errors.New("task run access denied")
	}

	cancelFunction := taskRunService.cancelFunctionForTaskRun(taskRun)
	now := time.Now()
	cancelledTaskRun, errorValue := taskRunService.TransitionTaskRun(agentcontract.TaskRunTransition{
		TaskRunID:             taskRunID,
		FromStates:            cancelTaskRunFromStates(),
		ToState:               agentcontract.TaskStatusCancelled,
		FailureReason:         strings.TrimSpace(reason),
		FinishCurrentAttempt:  true,
		FinishedAttemptStatus: agentcontract.TaskAttemptStatusCancelled,
		RunnerID:              taskRunService.runnerID,
		Event:                 newTaskRunTransitionEvent(taskRunID, agentcontract.TaskStatusCancelled, firstNonEmptyTaskRunString(reason, requesterPersonID), now),
		UpdatedAt:             now,
	})
	if errorValue != nil {
		return agentcontract.TaskRun{}, errorValue
	}
	if cancelFunction != nil {
		cancelFunction()
	}
	return cancelledTaskRun, nil
}

func (taskRunService *TaskRunService) cancelFunctionForTaskRun(taskRun agentcontract.TaskRun) context.CancelFunc {
	taskRunService.mutex.RLock()
	defer taskRunService.mutex.RUnlock()
	if !taskRunService.taskRunHasActiveAttemptLocked(taskRun) {
		return nil
	}
	activeAttempt := taskRunService.activeAttempts[taskRun.CurrentAttemptID]
	return activeAttempt.CancelFunction
}

func (taskRunService *TaskRunService) startTaskRunAttempt(taskRun agentcontract.TaskRun, taskAttempt agentcontract.TaskAttempt) error {
	if taskRunService.repository == nil {
		return nil
	}
	return taskRunService.repository.StartTaskRunAttempt(taskRun, taskAttempt)
}

func (taskRunService *TaskRunService) finishCurrentAttemptLocked(taskRun agentcontract.TaskRun, status agentcontract.TaskAttemptStatus, reason string) (context.CancelFunc, error) {
	taskAttemptID := strings.TrimSpace(taskRun.CurrentAttemptID)
	if taskAttemptID == "" {
		if errorValue := taskRunService.saveTaskRun(taskRun); errorValue != nil {
			return nil, errorValue
		}
		taskRunService.closeOpenToolRequests(taskRun.TaskRunID, "", "cancelled_by_attempt_end")
		return nil, nil
	}
	taskAttempt := taskRunService.findTaskAttemptForMutation(taskAttemptID, taskRun.TaskRunID)
	now := time.Now()
	taskAttempt.Status = status
	taskAttempt.FinishedAt = &now
	taskAttempt.FailureReason = strings.TrimSpace(reason)
	if errorValue := taskRunService.finishTaskRunAttempt(taskRun, taskAttempt); errorValue != nil {
		return nil, errorValue
	}
	taskRunService.taskAttempts[taskAttemptID] = taskAttempt
	activeAttempt := taskRunService.activeAttempts[taskAttemptID]
	delete(taskRunService.activeAttempts, taskAttemptID)
	taskRunService.closeOpenToolRequests(taskRun.TaskRunID, taskAttemptID, "cancelled_by_attempt_end")
	return activeAttempt.CancelFunction, nil
}

func (taskRunService *TaskRunService) findTaskAttemptForMutation(taskAttemptID string, taskRunID string) agentcontract.TaskAttempt {
	taskAttempt, isFound := taskRunService.taskAttempts[taskAttemptID]
	if isFound {
		return taskAttempt
	}
	if taskRunService.repository != nil {
		taskAttempt, isFound, errorValue := taskRunService.repository.FindTaskAttempt(taskAttemptID)
		if errorValue == nil && isFound {
			taskRunService.taskAttempts[taskAttemptID] = taskAttempt
			return taskAttempt
		}
	}
	return agentcontract.TaskAttempt{
		TaskAttemptID: taskAttemptID,
		TaskRunID:     taskRunID,
		RunnerID:      taskRunService.runnerID,
		Status:        agentcontract.TaskAttemptStatusRunning,
		StartedAt:     time.Now(),
	}
}

func (taskRunService *TaskRunService) finishTaskRunAttempt(taskRun agentcontract.TaskRun, taskAttempt agentcontract.TaskAttempt) error {
	if taskRunService.repository == nil {
		return nil
	}
	return taskRunService.repository.FinishTaskRunAttempt(taskRun, taskAttempt)
}

func (taskRunService *TaskRunService) taskRunHasActiveAttemptLocked(taskRun agentcontract.TaskRun) bool {
	if taskRun.Status != agentcontract.TaskStatusRunning {
		return false
	}
	taskAttemptID := strings.TrimSpace(taskRun.CurrentAttemptID)
	if taskAttemptID == "" {
		return false
	}
	activeAttempt, isFound := taskRunService.activeAttempts[taskAttemptID]
	return isFound && activeAttempt.TaskRunID == taskRun.TaskRunID
}

func taskAttemptStatusForTaskStatus(status agentcontract.TaskStatus) agentcontract.TaskAttemptStatus {
	switch status {
	case agentcontract.TaskStatusCompleted:
		return agentcontract.TaskAttemptStatusCompleted
	case agentcontract.TaskStatusCancelled:
		return agentcontract.TaskAttemptStatusCancelled
	case agentcontract.TaskStatusFailed:
		return agentcontract.TaskAttemptStatusFailed
	default:
		return agentcontract.TaskAttemptStatusInterrupted
	}
}

type openToolRequest struct {
	ObservationID string
	ToolName      string
}

type toolEventBody struct {
	ObservationID string `json:"observationID"`
	ToolName      string `json:"toolName"`
}

func openToolRequests(taskEvents []agentcontract.TaskEvent) []openToolRequest {
	requests := []openToolRequest{}
	closedObservationIDs := map[string]bool{}
	for _, taskEvent := range taskEvents {
		toolName, isToolEvent := agentcontract.ToolTaskEventToolName(taskEvent.Name, ".requested")
		if isToolEvent {
			body := parseToolEventBody(taskEvent.Body)
			requests = append(requests, openToolRequest{ObservationID: firstNonEmptyTaskRunString(body.ObservationID, taskEvent.TaskEventID), ToolName: firstNonEmptyTaskRunString(body.ToolName, toolName)})
			continue
		}
		if _, isResult := agentcontract.ToolTaskEventToolName(taskEvent.Name, ".result"); isResult {
			body := parseToolEventBody(taskEvent.Body)
			if body.ObservationID != "" {
				closedObservationIDs[body.ObservationID] = true
			}
			continue
		}
		if _, isCancelled := agentcontract.ToolTaskEventToolName(taskEvent.Name, ".cancelled"); isCancelled {
			body := parseToolEventBody(taskEvent.Body)
			if body.ObservationID != "" {
				closedObservationIDs[body.ObservationID] = true
			}
		}
	}
	openRequests := []openToolRequest{}
	for _, request := range requests {
		if !closedObservationIDs[request.ObservationID] {
			openRequests = append(openRequests, request)
		}
	}
	return openRequests
}

func parseToolEventBody(body string) toolEventBody {
	var parsedBody toolEventBody
	_ = json.Unmarshal([]byte(body), &parsedBody)
	return parsedBody
}

func marshalTaskRunServiceEventBody(value any) string {
	document, errorValue := json.Marshal(value)
	if errorValue != nil {
		return "{}"
	}
	return string(document)
}

func defaultTaskRunnerID() string {
	hostname, _ := os.Hostname()
	return firstNonEmptyTaskRunString(strings.TrimSpace(hostname), "unknown-host") + ":" + strconv.Itoa(os.Getpid())
}

func firstNonEmptyTaskRunString(values ...string) string {
	for _, value := range values {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}

func (taskRunService *TaskRunService) findTaskRunForMutation(taskRunID string) (agentcontract.TaskRun, bool) {
	taskRun, isFound := taskRunService.taskRuns[taskRunID]
	if isFound || taskRunService.repository == nil {
		return taskRun, isFound
	}
	taskRun, isFound, errorValue := taskRunService.repository.FindTaskRun(taskRunID)
	if errorValue != nil || !isFound {
		return agentcontract.TaskRun{}, false
	}
	taskRunService.taskRuns[taskRunID] = taskRun
	return taskRun, true
}

func (taskRunService *TaskRunService) PruneTerminalTaskRunsBefore(cutoff time.Time) []string {
	terminalStatuses := terminalTaskRunStatusStrings()
	if taskRunService.repository != nil {
		deletedIDs, errorValue := taskRunService.repository.DeleteTaskRunsBefore(cutoff, terminalStatuses)
		if errorValue == nil {
			taskRunService.evictTaskRunIDs(deletedIDs)
			return deletedIDs
		}
	}
	return taskRunService.pruneTerminalTaskRunsFromMemory(cutoff, terminalStatuses)
}

func (taskRunService *TaskRunService) pruneTerminalTaskRunsFromMemory(cutoff time.Time, terminalStatuses []string) []string {
	terminalStatusSet := map[string]bool{}
	for _, status := range terminalStatuses {
		terminalStatusSet[status] = true
	}
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	prunedIDs := []string{}
	for taskRunID, taskRun := range taskRunService.taskRuns {
		if terminalStatusSet[string(taskRun.Status)] && taskRun.UpdatedAt.Before(cutoff) {
			prunedIDs = append(prunedIDs, taskRunID)
		}
	}
	taskRunService.evictTaskRunIDsLocked(prunedIDs)
	return prunedIDs
}

func (taskRunService *TaskRunService) deleteTerminalTaskRunFromMemory(taskRunID string) bool {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	taskRun, isFound := taskRunService.taskRuns[taskRunID]
	if !isFound || !isTerminalTaskRunStatus(taskRun.Status) {
		return false
	}
	taskRunService.evictTaskRunIDsLocked([]string{taskRunID})
	return true
}

func (taskRunService *TaskRunService) evictTaskRunIDs(taskRunIDs []string) {
	taskRunService.mutex.Lock()
	defer taskRunService.mutex.Unlock()
	taskRunService.evictTaskRunIDsLocked(taskRunIDs)
}

func (taskRunService *TaskRunService) evictTaskRunIDsLocked(taskRunIDs []string) {
	for _, taskRunID := range taskRunIDs {
		delete(taskRunService.taskRuns, taskRunID)
	}
	for taskAttemptID, taskAttempt := range taskRunService.taskAttempts {
		for _, taskRunID := range taskRunIDs {
			if taskAttempt.TaskRunID == taskRunID {
				delete(taskRunService.taskAttempts, taskAttemptID)
				break
			}
		}
	}
	for taskAttemptID, activeAttempt := range taskRunService.activeAttempts {
		for _, taskRunID := range taskRunIDs {
			if activeAttempt.TaskRunID == taskRunID {
				delete(taskRunService.activeAttempts, taskAttemptID)
				break
			}
		}
	}
}

func terminalTaskRunStatusStrings() []string {
	return []string{
		string(agentcontract.TaskStatusCompleted),
		string(agentcontract.TaskStatusFailed),
		string(agentcontract.TaskStatusCancelled),
		string(agentcontract.TaskStatusBlocked),
	}
}

func isTerminalTaskRunStatus(status agentcontract.TaskStatus) bool {
	for _, terminalStatus := range terminalTaskRunStatusStrings() {
		if string(status) == terminalStatus {
			return true
		}
	}
	return false
}
