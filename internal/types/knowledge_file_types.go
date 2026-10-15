package types

import "strings"

// NormalizeKnowledgeFileTypes preserves distinct extensions (jpg != jpeg).
func NormalizeKnowledgeFileTypes(values []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, raw := range values {
		ext := strings.TrimLeft(strings.ToLower(strings.TrimSpace(raw)), ".")
		if ext != "" && !seen[ext] {
			seen[ext] = true
			result = append(result, ext)
		}
	}
	return result
}
