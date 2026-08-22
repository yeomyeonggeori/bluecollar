package tape

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/yeomyeonggeori/bluecollar/model"
)

const (
	KindText       = "text"
	KindStructured = "structured"
)

type Call struct {
	Index      int                      `json:"index"`
	Kind       string                   `json:"kind"`
	SchemaName string                   `json:"schemaName,omitempty"`
	Prompt     string                   `json:"prompt,omitempty"`
	Messages   []model.Message          `json:"messages,omitempty"`
	Text       string                   `json:"text,omitempty"`
	Response   model.StructuredResponse `json:"response,omitempty"`
	Failure    string                   `json:"failure,omitempty"`
}

type Recorder struct {
	languageModel model.LanguageModelProvider
	mutex         sync.Mutex
	writer        io.Writer
	nextIndex     int
}

func NewRecorder(languageModel model.LanguageModelProvider, writer io.Writer) *Recorder {
	return &Recorder{languageModel: languageModel, writer: writer}
}

func (recorder *Recorder) GenerateResponse(ctx context.Context, prompt string) (string, error) {
	text, errorValue := recorder.languageModel.GenerateResponse(ctx, prompt)
	recorder.write(Call{Kind: KindText, Prompt: prompt, Text: text, Failure: failureText(errorValue)})
	return text, errorValue
}

func (recorder *Recorder) GenerateStructuredResponse(ctx context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	response, errorValue := recorder.languageModel.GenerateStructuredResponse(ctx, request)
	recorder.write(Call{
		Kind:       KindStructured,
		SchemaName: request.StructuredOutputSchema.Name,
		Messages:   request.Messages,
		Response:   response,
		Failure:    failureText(errorValue),
	})
	return response, errorValue
}

func (recorder *Recorder) write(call Call) {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	call.Index = recorder.nextIndex
	recorder.nextIndex++
	document, errorValue := json.Marshal(call)
	if errorValue != nil {
		return
	}
	recorder.writer.Write(append(document, '\n'))
}

type Player struct {
	mutex     sync.Mutex
	calls     []Call
	nextIndex int
}

func Read(reader io.Reader) (*Player, error) {
	player := &Player{}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 32*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		call := Call{}
		if errorValue := json.Unmarshal([]byte(line), &call); errorValue != nil {
			return nil, errors.New("tape line " + strconv.Itoa(len(player.calls)) + " is not a recorded call: " + errorValue.Error())
		}
		player.calls = append(player.calls, call)
	}
	if errorValue := scanner.Err(); errorValue != nil {
		return nil, errorValue
	}
	return player, nil
}

func (player *Player) GenerateResponse(_ context.Context, prompt string) (string, error) {
	call, errorValue := player.take(KindText, "")
	if errorValue != nil {
		return "", errorValue
	}
	if call.Failure != "" {
		return "", errors.New(call.Failure)
	}
	_ = prompt
	return call.Text, nil
}

func (player *Player) GenerateStructuredResponse(_ context.Context, request model.StructuredResponseRequest) (model.StructuredResponse, error) {
	call, errorValue := player.take(KindStructured, request.StructuredOutputSchema.Name)
	if errorValue != nil {
		return model.StructuredResponse{}, errorValue
	}
	if call.Failure != "" {
		return model.StructuredResponse{}, errors.New(call.Failure)
	}
	return call.Response, nil
}

// A tape that no longer answers the calls the loop makes has stopped describing this
// loop, and saying so is the whole value of replaying it.
func (player *Player) take(kind string, schemaName string) (Call, error) {
	player.mutex.Lock()
	defer player.mutex.Unlock()
	if player.nextIndex >= len(player.calls) {
		return Call{}, errors.New("the tape has " + strconv.Itoa(len(player.calls)) + " calls and the loop asked for one more")
	}
	call := player.calls[player.nextIndex]
	player.nextIndex++
	if call.Kind != kind {
		return Call{}, errors.New("tape call " + strconv.Itoa(call.Index) + " recorded a " + call.Kind + " call and the loop made a " + kind + " one")
	}
	if kind == KindStructured && strings.TrimSpace(call.SchemaName) != strings.TrimSpace(schemaName) {
		return Call{}, errors.New("tape call " + strconv.Itoa(call.Index) + " recorded schema " + call.SchemaName + " and the loop asked for " + schemaName)
	}
	return call, nil
}

func (player *Player) Remaining() int {
	player.mutex.Lock()
	defer player.mutex.Unlock()
	return len(player.calls) - player.nextIndex
}

func failureText(errorValue error) string {
	if errorValue == nil {
		return ""
	}
	return errorValue.Error()
}
