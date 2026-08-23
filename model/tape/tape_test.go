package tape

import (
	"bytes"
	"context"
	"errors"
	"io"
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

type structuredOnlyProvider struct{}

func (structuredOnlyProvider) GenerateResponse(context.Context, string) (string, error) {
	return "", nil
}

func (structuredOnlyProvider) GenerateStructuredResponse(context.Context, model.StructuredResponseRequest) (model.StructuredResponse, error) {
	return model.StructuredResponse{}, nil
}

type chatCapableProvider struct {
	structuredOnlyProvider
	calls int
}

func (provider *chatCapableProvider) GenerateChatCompletion(context.Context, model.ChatCompletionRequest) (model.ChatCompletionResponse, error) {
	provider.calls++
	return model.ChatCompletionResponse{}, nil
}

func TestRecordingAProviderKeepsTheChatPathItWraps(t *testing.T) {
	provider := &chatCapableProvider{}
	if _, isAvailable := model.ResolveTextChatCompleter(provider); !isAvailable {
		t.Fatal("the provider under test has to offer the path this is about")
	}

	recorder := NewRecorder(provider, io.Discard)

	completer, isAvailable := model.ResolveTextChatCompleter(recorder)
	if !isAvailable {
		t.Fatal("wrapping a provider to watch it must not decide the loop takes the other path")
	}
	if _, errorValue := completer.GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{}); errorValue != nil {
		t.Fatalf("expected the call to reach the wrapped provider: %v", errorValue)
	}
	if provider.calls != 1 {
		t.Fatalf("the recorder has to delegate rather than answer, got %d calls through", provider.calls)
	}
}

func TestRecordingAProviderWithNoChatPathOffersNone(t *testing.T) {
	if _, isAvailable := model.ResolveTextChatCompleter(NewRecorder(structuredOnlyProvider{}, io.Discard)); isAvailable {
		t.Fatal("a recorder cannot offer a path the provider it wraps does not have")
	}
}

func TestATapeOfChatCallsIsReplayedDownTheChatPath(t *testing.T) {
	recorded := &bytes.Buffer{}
	recorder := NewRecorder(&chatCapableProvider{}, recorded)
	completer, _ := model.ResolveTextChatCompleter(recorder)
	if _, errorValue := completer.GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{SchemaName: "an_action"}); errorValue != nil {
		t.Fatalf("recording the call failed: %v", errorValue)
	}

	player, errorValue := Read(bytes.NewReader(recorded.Bytes()))
	if errorValue != nil {
		t.Fatalf("reading the tape back failed: %v", errorValue)
	}

	replay, isAvailable := model.ResolveTextChatCompleter(player)
	if !isAvailable {
		t.Fatal("a tape that holds chat calls has to be replayable where they were recorded")
	}
	if _, errorValue := replay.GenerateChatCompletion(context.Background(), model.ChatCompletionRequest{SchemaName: "an_action"}); errorValue != nil {
		t.Fatalf("expected the recorded chat call back: %v", errorValue)
	}
}

func TestATapeWithNoChatCallsOffersNoChatPath(t *testing.T) {
	player, errorValue := Read(strings.NewReader(`{"index":0,"kind":"structured","schemaName":"a_schema"}`))
	if errorValue != nil {
		t.Fatalf("reading the tape failed: %v", errorValue)
	}
	if _, isAvailable := model.ResolveTextChatCompleter(player); isAvailable {
		t.Fatal("a structured tape replayed down the chat path answers calls it never recorded")
	}
}
