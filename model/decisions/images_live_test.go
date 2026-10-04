//go:build llmeval

package decisions

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/yeomyeonggeori/bluecollar/evaltest"
	"github.com/yeomyeonggeori/bluecollar/model"
)

func solidColorPNG(t *testing.T, fill color.Color) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 1600, 900))
	for y := 0; y < 900; y++ {
		for x := 0; x < 1600; x++ {
			canvas.Set(x, y, fill)
		}
	}
	var encoded bytes.Buffer
	if errorValue := png.Encode(&encoded, canvas); errorValue != nil {
		t.Fatal(errorValue)
	}
	return encoded.Bytes()
}

func TestLiveDecisionReadsImageBackground(t *testing.T) {
	apiKey := evaltest.RequireInput(t, apiKeyEnvironmentName, "use an OpenRouter key")
	modelName := evaltest.RequireInput(t, modelEnvironmentName, "use cloudflare/clef-flash")
	decisionModel := Endpoint{URL: DefaultEndpointURL, ModelName: modelName, APIKey: apiKey}.DecisionModel()
	question := model.ChoiceQuestion{
		Instructions: "What color is the background of this image?",
		OptionDescriptions: map[string]string{
			"yellow": "the background is yellow",
			"white":  "the background is white",
			"navy":   "the background is navy blue",
			"green":  "the background is green",
		},
	}.Question()
	cases := []struct {
		wantedOption string
		fill         color.Color
	}{
		{"yellow", color.RGBA{R: 255, G: 244, B: 170, A: 255}},
		{"navy", color.RGBA{R: 15, G: 25, B: 70, A: 255}},
	}
	for _, liveCase := range cases {
		response, errorValue := decisionModel.Decide(context.Background(), model.DecisionRequest{
			State:     "A rendered slide.",
			Questions: map[string]model.DecisionQuestion{"background": question},
			Images:    []model.DecisionImage{{MediaType: "image/png", Data: solidColorPNG(t, liveCase.fill)}},
		})
		if errorValue != nil {
			t.Fatal(errorValue)
		}
		probability := response.Answers["background"].ChoiceProbability(liveCase.wantedOption)
		t.Logf("%s: probabilities %v, input tokens %d", liveCase.wantedOption, response.Answers["background"].Probabilities, response.Usage.PromptTokens)
		if probability < 0.8 {
			t.Errorf("%s probability = %.3f, want >= 0.8", liveCase.wantedOption, probability)
		}
	}
}
