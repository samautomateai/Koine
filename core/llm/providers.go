package llm

import "strings"

// The wire protocols Koine speaks, named as Pi names them in models.json (`api`).
const (
	APIAnthropic   = "anthropic-messages"
	APICompletions = "openai-completions"
	APIResponses   = "openai-responses"
	APIAzure       = "azure-openai-responses"
	APIGemini      = "google-generative-ai"
)

// APIs lists the protocols a provider can be configured with.
var APIs = []string{APIAnthropic, APICompletions, APIResponses, APIAzure, APIGemini}

// ProviderInfo describes a built-in provider.
type ProviderInfo struct {
	ID      string
	Name    string
	API     string
	BaseURL string   // "" = the endpoint comes from the environment or models.json (Azure)
	Env     []string // variables that hold the API key, in order; the first one set wins (names are Pi's)
	Local   bool     // a local server: no key is needed
}

// providerTable is the built-in provider list. Environment variable names are Pi's (https://pi.dev/docs/latest/providers). The base URLs
// are the vendors' public endpoints as the authors know them; they were not compared with Pi's own registry, and a wrong one is fixed
// with `baseUrl` in models.json.
var providerTable = []ProviderInfo{
	{ID: "anthropic", Name: "Anthropic", API: APIAnthropic, BaseURL: "https://api.anthropic.com", Env: []string{"ANTHROPIC_API_KEY"}},
	{ID: "openai", Name: "OpenAI", API: APICompletions, BaseURL: "https://api.openai.com/v1", Env: []string{"OPENAI_API_KEY"}},
	{ID: "azure", Name: "Azure OpenAI", API: APIAzure, Env: []string{"AZURE_OPENAI_API_KEY"}},
	{ID: "google", Name: "Google Gemini", API: APIGemini, BaseURL: "https://generativelanguage.googleapis.com/v1beta", Env: []string{"GEMINI_API_KEY"}},
	{ID: "mistral", Name: "Mistral", API: APICompletions, BaseURL: "https://api.mistral.ai/v1", Env: []string{"MISTRAL_API_KEY"}},
	{ID: "groq", Name: "Groq", API: APICompletions, BaseURL: "https://api.groq.com/openai/v1", Env: []string{"GROQ_API_KEY"}},
	{ID: "cerebras", Name: "Cerebras", API: APICompletions, BaseURL: "https://api.cerebras.ai/v1", Env: []string{"CEREBRAS_API_KEY"}},
	{ID: "xai", Name: "xAI", API: APICompletions, BaseURL: "https://api.x.ai/v1", Env: []string{"XAI_API_KEY"}},
	{ID: "openrouter", Name: "OpenRouter", API: APICompletions, BaseURL: "https://openrouter.ai/api/v1", Env: []string{"OPENROUTER_API_KEY"}},
	{ID: "deepseek", Name: "DeepSeek", API: APICompletions, BaseURL: "https://api.deepseek.com/v1", Env: []string{"DEEPSEEK_API_KEY"}},
	{ID: "together", Name: "Together AI", API: APICompletions, BaseURL: "https://api.together.xyz/v1", Env: []string{"TOGETHER_API_KEY"}},
	{ID: "fireworks", Name: "Fireworks", API: APICompletions, BaseURL: "https://api.fireworks.ai/inference/v1", Env: []string{"FIREWORKS_API_KEY"}},
	{ID: "huggingface", Name: "Hugging Face", API: APICompletions, BaseURL: "https://router.huggingface.co/v1", Env: []string{"HF_TOKEN"}},
	{ID: "nvidia", Name: "NVIDIA NIM", API: APICompletions, BaseURL: "https://integrate.api.nvidia.com/v1", Env: []string{"NVIDIA_API_KEY"}},
	{ID: "moonshot", Name: "Moonshot AI", API: APICompletions, BaseURL: "https://api.moonshot.ai/v1", Env: []string{"MOONSHOT_API_KEY"}},
	{ID: "vercel-ai-gateway", Name: "Vercel AI Gateway", API: APICompletions, BaseURL: "https://ai-gateway.vercel.sh/v1",
		Env: []string{"AI_GATEWAY_API_KEY"}},
	{ID: "zai", Name: "Z.AI Coding Plan", API: APICompletions, BaseURL: "https://api.z.ai/api/coding/paas/v4", Env: []string{"ZAI_API_KEY"}},
	{ID: "ollama", Name: "Ollama", API: APICompletions, BaseURL: "http://localhost:11434/v1", Local: true},
	{ID: "lmstudio", Name: "LM Studio", API: APICompletions, BaseURL: "http://localhost:1234/v1", Local: true},
	{ID: "vllm", Name: "vLLM", API: APICompletions, BaseURL: "http://localhost:8000/v1", Local: true},
	{ID: "sglang", Name: "SGLang", API: APICompletions, BaseURL: "http://localhost:30000/v1", Local: true},
	{ID: "llamacpp", Name: "llama.cpp", API: APICompletions, BaseURL: "http://localhost:8080/v1", Local: true},
}

// providerAliases are other spellings of built-in ids: Pi renamed Azure's, and "gemini" is what people type.
var providerAliases = map[string]string{"azure-openai-responses": "azure", "gemini": "google"}

// Providers returns the built-in providers in table order.
func Providers() []ProviderInfo { return append([]ProviderInfo(nil), providerTable...) }

// BuiltinProvider finds a built-in provider by id or alias.
func BuiltinProvider(id string) (ProviderInfo, bool) {
	if a, ok := providerAliases[id]; ok {
		id = a
	}
	for _, p := range providerTable {
		if p.ID == id {
			return p, true
		}
	}
	return ProviderInfo{}, false
}

// CanonicalProvider maps an alias to the id Koine stores credentials under.
func CanonicalProvider(id string) string {
	if a, ok := providerAliases[id]; ok {
		return a
	}
	return id
}

func isOpenAIName(m string) bool {
	for _, p := range []string{"gpt-", "chatgpt-", "o1", "o3", "o4"} {
		if strings.HasPrefix(m, p) {
			return true
		}
	}
	return false
}

// inferProvider guesses the provider of a bare model id. It only recognises the names vendors use for their own models.
func inferProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "claude"):
		return "anthropic"
	case isOpenAIName(model):
		return "openai"
	case strings.HasPrefix(model, "gemini-"):
		return "google"
	}
	return ""
}
