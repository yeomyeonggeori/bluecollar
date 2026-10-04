package visualcheck

import (
	"encoding/json"
	"fmt"
)

type Question struct {
	Instructions string            `json:"instructions"`
	Options      map[string]string `json:"options"`
	CleanOption  string            `json:"cleanOption"`
}

type Fixer struct {
	Instructions string `json:"instructions"`
	KitGuide     string `json:"kitGuide"`
}

type MeasuredDefect struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Suggestion string `json:"suggestion"`
}

type Slide struct {
	Number   int              `json:"number"`
	Image    string           `json:"image"`
	State    map[string]any   `json:"state"`
	Section  string           `json:"section"`
	Measured []MeasuredDefect `json:"measured"`
}

type Manifest struct {
	Question          Question           `json:"question"`
	Threshold         float64            `json:"threshold"`
	PatternThresholds map[string]float64 `json:"patternThresholds,omitempty"`
	Rounds            int                `json:"rounds"`
	Fixer             Fixer              `json:"fixer"`
	Source            string             `json:"source"`
	Slides            []Slide            `json:"slides"`
}

func (manifest Manifest) thresholdFor(option string) float64 {
	if threshold, hasOwn := manifest.PatternThresholds[option]; hasOwn {
		return threshold
	}
	return manifest.Threshold
}

func ParseManifest(content []byte) (Manifest, error) {
	var manifest Manifest
	if errorValue := json.Unmarshal(content, &manifest); errorValue != nil {
		return Manifest{}, fmt.Errorf("visual review manifest is not valid JSON: %w", errorValue)
	}
	if errorValue := manifest.validate(); errorValue != nil {
		return Manifest{}, errorValue
	}
	return manifest, nil
}

func (manifest Manifest) validate() error {
	if _, hasCleanOption := manifest.Question.Options[manifest.Question.CleanOption]; !hasCleanOption {
		return fmt.Errorf("visual review manifest: cleanOption %q is not one of the options", manifest.Question.CleanOption)
	}
	if manifest.Threshold <= 0 || manifest.Threshold >= 1 {
		return fmt.Errorf("visual review manifest: threshold %v is not between 0 and 1", manifest.Threshold)
	}
	for option, threshold := range manifest.PatternThresholds {
		if _, isOption := manifest.Question.Options[option]; !isOption || option == manifest.Question.CleanOption {
			return fmt.Errorf("visual review manifest: patternThresholds names %q, which is not a defect option", option)
		}
		if threshold <= 0 || threshold >= 1 {
			return fmt.Errorf("visual review manifest: patternThresholds[%q] %v is not between 0 and 1", option, threshold)
		}
	}
	if manifest.Rounds < 0 {
		return fmt.Errorf("visual review manifest: rounds %d is negative", manifest.Rounds)
	}
	return nil
}

func (manifest Manifest) slidesNumbered(numbers map[int]string) []Slide {
	slides := []Slide{}
	for _, slide := range manifest.Slides {
		if _, isWanted := numbers[slide.Number]; isWanted {
			slides = append(slides, slide)
		}
	}
	return slides
}

func (manifest Manifest) sectionsByNumber() map[int]string {
	sections := map[int]string{}
	for _, slide := range manifest.Slides {
		sections[slide.Number] = slide.Section
	}
	return sections
}
