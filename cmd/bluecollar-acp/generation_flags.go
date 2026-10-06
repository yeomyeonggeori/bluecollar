package main

import (
	"os"
	"strconv"

	"github.com/yeomyeonggeori/blueprotocol/model"
)

type generationFlags struct {
	seed        *int64
	temperature *float64
}

func (flags *generationFlags) setSeed(value string) error {
	seed, errorValue := strconv.ParseInt(value, 10, 64)
	if errorValue != nil {
		return errorValue
	}
	flags.seed = &seed
	return nil
}

func (flags *generationFlags) setTemperature(value string) error {
	temperature, errorValue := strconv.ParseFloat(value, 64)
	if errorValue != nil {
		return errorValue
	}
	flags.temperature = &temperature
	return nil
}

func (flags *generationFlags) setFromEnvironment() {
	if value := os.Getenv("BLUECOLLAR_SEED"); value != "" {
		_ = flags.setSeed(value)
	}
	if value := os.Getenv("BLUECOLLAR_TEMPERATURE"); value != "" {
		_ = flags.setTemperature(value)
	}
}

func (flags generationFlags) options() model.GenerationOptions {
	return model.GenerationOptions{Seed: flags.seed, Temperature: flags.temperature}
}
