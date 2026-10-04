package toolcontract

import (
	"strings"
	"testing"
)

func TestASourceFromASchemaOrADeclarationIsOneWhoseLayoutCodeOwns(t *testing.T) {
	for _, document := range []string{
		`{"schema":"report","given":{},"claims":[{"path":"title","text":"Report"}]}`,
		`{"declaration":{"kind":"workbook"},"claims":[{"path":"views[0].title","text":"By quarter"}]}`,
	} {
		source, isParsed := DeliveredSourceOf([]byte(document))
		if !isParsed || !source.IsLayoutOwnedByCode {
			t.Fatalf("expected a code-owned layout for %s, got %+v", document, source)
		}
	}
	source, isParsed := DeliveredSourceOf([]byte(`{"command":"office create","claims":[{"path":"slide1","text":"Q3 grew 12%"}]}`))
	if !isParsed || source.IsLayoutOwnedByCode {
		t.Fatalf("expected a file whose layout the model wrote, got %+v", source)
	}
}

func TestASourceWithoutClaimsOrTooLargeIsNotRead(t *testing.T) {
	if _, isParsed := DeliveredSourceOf([]byte(`{"schema":"report","given":{}}`)); isParsed {
		t.Fatal("a source without claims has nothing to ask")
	}
	oversized := `{"schema":"report","claims":[{"path":"title","text":"` + strings.Repeat("x", DeliveredSourceMaximumBytes) + `"}]}`
	if _, isParsed := DeliveredSourceOf([]byte(oversized)); isParsed {
		t.Fatal("an oversized source is not read")
	}
}

func TestKnownValuesTooLargeToShowAreLeftOut(t *testing.T) {
	document := `{"schema":"report","known":{"company":"` + strings.Repeat("y", deliveredKnownMaximumBytes) + `"},"claims":[{"path":"title","text":"Report"}]}`
	source, _ := DeliveredSourceOf([]byte(document))
	if source.Known != nil {
		t.Fatal("expected oversized known values to be left out")
	}
}
