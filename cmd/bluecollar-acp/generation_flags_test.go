package main

import "testing"

func TestGenerationFlagsCarryTheSeedAndTemperatureTheHostSet(t *testing.T) {
	flags := generationFlags{}

	if errorValue := flags.setSeed("41"); errorValue != nil {
		t.Fatal(errorValue)
	}
	if errorValue := flags.setTemperature("0.2"); errorValue != nil {
		t.Fatal(errorValue)
	}

	options := flags.options()
	if options.Seed == nil || *options.Seed != 41 || options.Temperature == nil || *options.Temperature != 0.2 {
		t.Fatalf("expected seed 41 and temperature 0.2, got %+v", options)
	}
}

func TestGenerationFlagsLeaveWhatTheHostDidNotSetUnset(t *testing.T) {
	options := generationFlags{}.options()

	if options.Seed != nil || options.Temperature != nil {
		t.Fatalf("expected nothing set, got %+v", options)
	}
}
