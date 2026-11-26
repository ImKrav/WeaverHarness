package provider

import "testing"

func TestClassify(t *testing.T) {
	tests := []struct {
		name string
		text string
		want ErrClass
	}{
		{name: "usage limit", text: "Claude AI Usage Limit reached", want: QuotaExhausted},
		{name: "rate limit", text: "error 429 rate limit exceeded", want: QuotaExhausted},
		{name: "quota exhausted", text: "Your Quota has been EXHAUSTED", want: QuotaExhausted},
		{name: "not logged in", text: "Not logged in · Please run /login", want: NotLoggedIn},
		{name: "login required", text: "Authentication failed: login required", want: NotLoggedIn},
		{name: "unauthorized", text: "HTTP 401 Unauthorized", want: NotLoggedIn},
		{name: "unknown model", text: "Error: Unknown model foo-bar", want: ModelUnknown},
		{name: "model not found", text: "model not found: x", want: ModelUnknown},
		{name: "invalid model", text: "Invalid model specified", want: ModelUnknown},
		{name: "codex model not supported", text: "The 'modelo-inexistente' model is not supported when using Codex with a ChatGPT account.", want: ModelUnknown},
		{name: "fallback crashed", text: "unexpected segmentation fault", want: Crashed},
		{name: "empty string", text: "", want: Crashed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Classify(tt.text); got != tt.want {
				t.Fatalf("Classify(%q) = %q, want %q", tt.text, got, tt.want)
			}
		})
	}
}
