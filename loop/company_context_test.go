package loop

import (
	"strings"
	"testing"
)

func TestCompanyContextRendersIdentityAndSelfUpdateRule(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{
		Company: CompanyContext{
			Name:           "주식회사 예시상사",
			BrandName:      "예시인턴",
			Slogan:         "모두에게 예시 인턴을",
			Description:    "예시 서비스를 만드는 테스트 회사",
			Representative: "최예제",
			Website:        "https://example.com",
		},
	})
	for _, expected := range []string{
		"Our company:",
		"주식회사 예시상사 (brand: 예시인턴) — 모두에게 예시 인턴을",
		"represented by 최예제",
		"company_info_get",
		"company_metric_record",
	} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("company context missing %q in:\n%s", expected, contextText)
		}
	}
}

func TestCompanyContextEmptyStateSaysSoAndAsksRatherThanInventing(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{})
	for _, expected := range []string{"Our company:", "Not registered yet", "ask once"} {
		if !strings.Contains(contextText, expected) {
			t.Fatalf("empty company state missing %q in:\n%s", expected, contextText)
		}
	}
}

func TestCompanyContextNamesNoToolThatDoesNotExist(t *testing.T) {
	contextText := (LLMContextBuilder{}).Build(LLMContextInput{})
	for _, absent := range []string{"company_info_set", "company_metric_record", "company.record.add"} {
		if strings.Contains(contextText, absent) {
			t.Fatalf("no such tool is registered anywhere, so every call carried an instruction the model could not follow: %q", absent)
		}
	}
}

func TestAgentKernelCompanyProviderFeedsTurnRequest(t *testing.T) {
	agentKernel := &AgentKernel{}
	agentKernel.UseCompanyProvider(func() CompanyContext {
		return CompanyContext{Name: "주식회사 예시상사"}
	})
	if agentKernel.companyContext().Name != "주식회사 예시상사" {
		t.Fatal("company provider not applied")
	}
	agentKernel = &AgentKernel{}
	if !agentKernel.companyContext().IsEmpty() {
		t.Fatal("missing provider should yield empty company")
	}
}
