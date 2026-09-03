package taskstate

import "time"

type TaskRunOrigin struct {
	ConversationID string
	ReplyTargetID  string
	IsThread       bool
}

type TaskRunCancelRequest struct {
	TaskRunIDs                 []string
	RequesterPersonID          string
	OriginConversationIDs      []string
	OriginConversationIDPrefix string
	ScheduleOnly               bool
	StaleBefore                *time.Time
	Reason                     string
}

type RawTurnEvent struct {
	TaskRunID string
	Name      string
	Body      string
}
