package trace

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/yeomyeonggeori/bluecollar/bench"
	"github.com/yeomyeonggeori/bluecollar/taskstate"
)

const PrivacyNotice = "This trace carries whatever the task carried: message text, file contents and tool inputs, none of it removed. Read it before you send it anywhere."

type Bundle struct {
	TaskRunID     string           `json:"taskRunID"`
	Status        string           `json:"status,omitempty"`
	Prompt        string           `json:"prompt,omitempty"`
	Reply         string           `json:"reply,omitempty"`
	FailureReason string           `json:"failureReason,omitempty"`
	Metrics       bench.RunMetrics `json:"metrics"`
	Events        []Event          `json:"events"`
	PrivacyNotice string           `json:"privacyNotice"`
}

type Event struct {
	Name      string    `json:"name"`
	Body      string    `json:"body,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty"`
}

func Build(taskRun taskstate.TaskRun, taskEvents []taskstate.TaskEvent, reply string) Bundle {
	events := make([]Event, 0, len(taskEvents))
	for _, taskEvent := range taskEvents {
		events = append(events, Event{
			Name:      strings.TrimSpace(taskEvent.Name),
			Body:      strings.TrimSpace(taskEvent.Body),
			CreatedAt: taskEvent.CreatedAt,
		})
	}
	return Bundle{
		TaskRunID:     strings.TrimSpace(taskRun.TaskRunID),
		Status:        string(taskRun.Status),
		Prompt:        strings.TrimSpace(taskRun.Prompt),
		Reply:         strings.TrimSpace(reply),
		FailureReason: strings.TrimSpace(taskRun.FailureReason),
		Metrics:       bench.MeasureTaskRun(taskRun.TaskRunID, taskEvents),
		Events:        events,
		PrivacyNotice: PrivacyNotice,
	}
}

func (bundle Bundle) JSON() ([]byte, error) {
	return json.MarshalIndent(bundle, "", "  ")
}

func (bundle Bundle) Markdown() string {
	sections := []string{
		"# Task run " + bundle.TaskRunID,
		"",
		PrivacyNotice,
		"",
		bundle.outcomeSection(),
		bundle.measurementSection(),
		bundle.ledgerSection(),
	}
	return strings.Join(sections, "\n")
}

func (bundle Bundle) outcomeSection() string {
	lines := []string{"## What was asked and what came back", ""}
	lines = append(lines, labelledBlock("Request", bundle.Prompt)...)
	lines = append(lines, labelledBlock("Reply", bundle.Reply)...)
	lines = append(lines, labelledBlock("Failure reason", bundle.FailureReason)...)
	lines = append(lines, "Status: "+firstNonEmpty(bundle.Status, "unknown"), "")
	return strings.Join(lines, "\n")
}

func (bundle Bundle) measurementSection() string {
	document, errorValue := json.MarshalIndent(bundle.Metrics, "", "  ")
	if errorValue != nil {
		return ""
	}
	return strings.Join([]string{"## What it cost", "", "```json", string(document), "```", ""}, "\n")
}

func (bundle Bundle) ledgerSection() string {
	lines := []string{"## Every step, in order", ""}
	for _, event := range bundle.Events {
		lines = append(lines, "### "+event.Name+timestampSuffix(event.CreatedAt))
		if event.Body != "" {
			lines = append(lines, "", "```", event.Body, "```")
		}
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

func labelledBlock(label string, body string) []string {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	return []string{label + ":", "", "```", strings.TrimSpace(body), "```", ""}
}

func timestampSuffix(createdAt time.Time) string {
	if createdAt.IsZero() {
		return ""
	}
	return " — " + createdAt.UTC().Format(time.RFC3339)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmedValue := strings.TrimSpace(value); trimmedValue != "" {
			return trimmedValue
		}
	}
	return ""
}
