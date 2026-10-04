package decisions

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func postedState(t *testing.T, request model.DecisionRequest) json.RawMessage {
	t.Helper()
	var posted struct {
		State json.RawMessage `json:"state"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, httpRequest *http.Request) {
		body, errorValue := io.ReadAll(httpRequest.Body)
		if errorValue != nil {
			t.Error(errorValue)
		}
		if errorValue := json.Unmarshal(body, &posted); errorValue != nil {
			t.Error(errorValue)
		}
		io.WriteString(responseWriter, `{"answers":{},"usage":{}}`)
	}))
	defer server.Close()
	endpoint := Endpoint{URL: server.URL, ModelName: "test-model", APIKey: "test-key"}
	if _, errorValue := endpoint.DecisionModel().Decide(context.Background(), request); errorValue != nil {
		t.Fatal(errorValue)
	}
	return posted.State
}

func TestDecideWithoutImagesPostsStateUnchanged(t *testing.T) {
	state := postedState(t, model.DecisionRequest{State: map[string]string{"slide": "one"}})
	if string(state) != `{"slide":"one"}` {
		t.Fatalf("posted state = %s", state)
	}
}

func TestDecideWithImagesPostsContentParts(t *testing.T) {
	request := model.DecisionRequest{
		State: "two slides",
		Images: []model.DecisionImage{
			{MediaType: "image/png", Data: []byte("first")},
			{MediaType: "image/jpeg", Data: []byte("second")},
		},
	}
	var parts []struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		ImageURL struct {
			URL string `json:"url"`
		} `json:"image_url"`
	}
	if errorValue := json.Unmarshal(postedState(t, request), &parts); errorValue != nil {
		t.Fatal(errorValue)
	}
	if len(parts) != 3 {
		t.Fatalf("posted %d parts, want 3", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != "two slides" {
		t.Fatalf("first part = %+v", parts[0])
	}
	if parts[1].Type != "image_url" || parts[1].ImageURL.URL != "data:image/png;base64,Zmlyc3Q=" {
		t.Fatalf("second part = %+v", parts[1])
	}
	if parts[2].Type != "image_url" || parts[2].ImageURL.URL != "data:image/jpeg;base64,c2Vjb25k" {
		t.Fatalf("third part = %+v", parts[2])
	}
}
