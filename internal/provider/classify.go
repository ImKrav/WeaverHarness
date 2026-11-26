package provider

import "strings"

var (
	quotaPatterns = []string{
		"usage limit",
		"rate limit",
		"hour limit",
		"quota",
		"exhausted",
		"429",
	}
	authPatterns = []string{
		"not logged in",
		"log in",
		"login required",
		"unauthorized",
		"authenticate",
	}
	modelPatterns = []string{
		"unknown model",
		"model not found",
		"invalid model",
		"model is not supported",
	}
)

// Classify maps provider error text into a normalized ErrClass.
func Classify(text string) ErrClass {
	lower := strings.ToLower(text)
	for _, p := range quotaPatterns {
		if strings.Contains(lower, p) {
			return QuotaExhausted
		}
	}
	for _, p := range authPatterns {
		if strings.Contains(lower, p) {
			return NotLoggedIn
		}
	}
	for _, p := range modelPatterns {
		if strings.Contains(lower, p) {
			return ModelUnknown
		}
	}
	return Crashed
}
