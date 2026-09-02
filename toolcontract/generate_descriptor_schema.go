//go:build ignore

package main

import (
	"log"
	"os"

	"github.com/yeomyeonggeori/bluecollar/toolcontract"
)

func main() {
	document, errorValue := toolcontract.ToolDescriptorSchemaDocument()
	if errorValue != nil {
		log.Fatal(errorValue)
	}
	if errorValue := os.WriteFile(toolcontract.DescriptorSchemaPath, append(document, '\n'), 0o644); errorValue != nil {
		log.Fatal(errorValue)
	}
}
