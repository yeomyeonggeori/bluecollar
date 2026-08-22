package tape

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

type scriptedLanguageModel struct {
	responses []model.StructuredResponse
	failures  []error
	index     int
}

func (languageModel *scriptedLanguageModel) GenerateResponse(context.Context, string) (string, error) {
	return "text reply", nil
}

func (languageModel *scriptedLanguageModel) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	index := languageModel.index
	languageModel.index++
	if index < len(languageModel.failures) && languageModel.failures[index] != nil {
		return model.StructuredResponse{}, languageModel.failures[index]
	}
	return languageModel.responses[index], nil
}

func TestAReplayedTurnSeesWhatTheRecordedOneSaw(t *testing.T) {
	tape := &bytes.Buffer{}
	recorder := NewRecorder(&scriptedLanguageModel{
		responses: []model.StructuredResponse{{Content: `{"action":"continue"}`}, {}},
		failures:  []error{nil, errors.New("the endpoint returned 502")},
	}, tape)

	structuredRequest := model.StructuredResponseRequest{
		Messages:               []model.Message{{Role: "system", Content: "you are the assistant"}},
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_agent_turn_action"},
	}
	recorder.GenerateStructuredResponse(context.Background(), structuredRequest)
	recorder.GenerateResponse(context.Background(), "write the reply")
	recorder.GenerateStructuredResponse(context.Background(), structuredRequest)

	player, errorValue := Read(bytes.NewReader(tape.Bytes()))
	if errorValue != nil {
		t.Fatalf("reading the tape failed: %v", errorValue)
	}

	response, errorValue := player.GenerateStructuredResponse(context.Background(), structuredRequest)
	if errorValue != nil || response.Content != `{"action":"continue"}` {
		t.Fatalf("the replayed call answers what the recorded one answered: %+v %v", response, errorValue)
	}
	if text, _ := player.GenerateResponse(context.Background(), "write the reply"); text != "text reply" {
		t.Fatalf("a text call replays too, got %q", text)
	}
	if _, errorValue := player.GenerateStructuredResponse(context.Background(), structuredRequest); errorValue == nil || !strings.Contains(errorValue.Error(), "502") {
		t.Fatalf("a recorded failure is part of what happened; a tape that only replays the happy path is not the run: %v", errorValue)
	}
	if player.Remaining() != 0 {
		t.Fatalf("the tape is spent, got %d left", player.Remaining())
	}
}

func TestATapeThatNoLongerAnswersTheLoopSaysSo(t *testing.T) {
	tape := &bytes.Buffer{}
	recorder := NewRecorder(&scriptedLanguageModel{responses: []model.StructuredResponse{{Content: "{}"}}}, tape)
	recorder.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_agent_turn_action"},
	})

	player, errorValue := Read(bytes.NewReader(tape.Bytes()))
	if errorValue != nil {
		t.Fatalf("reading the tape failed: %v", errorValue)
	}

	_, errorValue = player.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_execution_plan"},
	})
	if errorValue == nil || !strings.Contains(errorValue.Error(), "bluecollar_execution_plan") {
		t.Fatalf("a tape recorded against a different loop has stopped describing this one, and papering over that is how a replay proves nothing: %v", errorValue)
	}

	spentPlayer, _ := Read(bytes.NewReader(tape.Bytes()))
	spentPlayer.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_agent_turn_action"},
	})
	if _, errorValue := spentPlayer.GenerateStructuredResponse(context.Background(), model.StructuredResponseRequest{
		StructuredOutputSchema: model.StructuredOutputSchema{Name: "bluecollar_agent_turn_action"},
	}); errorValue == nil || !strings.Contains(errorValue.Error(), "asked for one more") {
		t.Fatalf("a loop that takes more steps than the tape recorded is a loop the tape cannot speak for: %v", errorValue)
	}
}
