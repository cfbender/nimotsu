package gmail

import (
	"regexp"
	"sort"
	"strings"
)

var (
	strongTrackingPattern  = regexp.MustCompile(`(?i)\b(?:1Z[0-9A-Z]{16}|TBA[0-9]{12}|[A-Z]{2}[0-9]{9}[A-Z]{2}|(?:92|93|94|95|96)[0-9]{18,20})\b`)
	labeledTrackingPattern = regexp.MustCompile(`(?i)(?:tracking(?:\s+(?:number|no\.?|id))?|track\s+id)\s*[:#-]?\s*([A-Z0-9][A-Z0-9-]{7,49})`)
)

func ExtractTrackingNumbers(text string) []string {
	normalized := strings.ToUpper(text)
	unique := make(map[string]struct{})
	for _, match := range strongTrackingPattern.FindAllString(normalized, -1) {
		unique[match] = struct{}{}
	}
	for _, match := range labeledTrackingPattern.FindAllStringSubmatch(normalized, -1) {
		candidate := strings.Trim(match[1], "- ")
		if plausibleCandidate(candidate) {
			unique[candidate] = struct{}{}
		}
	}
	result := make([]string, 0, len(unique))
	for candidate := range unique {
		result = append(result, candidate)
	}
	sort.Strings(result)
	return result
}

func plausibleCandidate(candidate string) bool {
	if len(candidate) < 8 || len(candidate) > 50 {
		return false
	}
	digits := 0
	for _, character := range candidate {
		if character >= '0' && character <= '9' {
			digits++
		}
	}
	return digits >= 5
}
