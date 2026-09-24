package agentcontract

import (
	"math"
	"math/rand/v2"

	"github.com/yeomyeonggeori/bluecollar/model"
)

func seededGenerationOptions(generationOptions model.GenerationOptions) model.GenerationOptions {
	if generationOptions.Seed != nil {
		return generationOptions
	}
	seed := int64(rand.Int32N(math.MaxInt32))
	generationOptions.Seed = &seed
	return generationOptions
}
