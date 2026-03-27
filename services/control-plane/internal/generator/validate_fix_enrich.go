package generator

import (
	"fmt"
	"sort"
	"strings"

	"github.com/meridianhub/meridian/shared/pkg/saga/schema"
	"github.com/meridianhub/meridian/shared/platform/events/topics"
)

// enrichErrors augments validation errors with additional context to help the LLM
// produce better fixes. When no schema registry is provided, errors are returned unchanged.
func enrichErrors(errs []ValidationError, registry *schema.Registry) []ValidationError {
	if len(errs) == 0 {
		return errs
	}

	enriched := make([]ValidationError, len(errs))
	copy(enriched, errs)

	for i := range enriched {
		switch enriched[i].Code {
		case "UNKNOWN_HANDLER":
			enrichUnknownHandler(&enriched[i], registry)
		case "UNKNOWN_EVENT_TOPIC":
			enrichUnknownEventTopic(&enriched[i])
		case "MISSING_REQUIRED_PARAM":
			enrichMissingRequiredParam(&enriched[i], registry)
		case "WRONG_PARAM_TYPE":
			enrichWrongParamType(&enriched[i], registry)
		}
	}

	return enriched
}

func enrichUnknownHandler(e *ValidationError, registry *schema.Registry) {
	if registry != nil {
		e.AvailableFields = registry.ListHandlers()
	}
}

func enrichUnknownEventTopic(e *ValidationError) {
	allTopics := topics.All()
	if e.Suggestion == "" {
		closest := findClosestTopicMatch(e.Message, allTopics)
		if closest != "" {
			e.Suggestion = closest
		}
	}
	if len(e.AvailableFields) == 0 {
		e.AvailableFields = allTopics
	}
}

func enrichMissingRequiredParam(e *ValidationError, registry *schema.Registry) {
	if registry == nil {
		return
	}
	handlerName := extractHandlerName(e.Path)
	if handlerName == "" {
		return
	}
	h, err := registry.GetHandler(handlerName)
	if err != nil {
		return
	}
	var required []string
	for paramName, field := range h.Params {
		if field.Required {
			required = append(required, fmt.Sprintf("%s (%s)", paramName, field.Type))
		}
	}
	sort.Strings(required)
	if len(required) > 0 {
		e.Message = fmt.Sprintf("%s. Required params: %s", e.Message, strings.Join(required, ", "))
	}
}

func enrichWrongParamType(e *ValidationError, registry *schema.Registry) {
	if registry == nil {
		return
	}
	handlerName := extractHandlerName(e.Path)
	paramName := extractParamName(e.Path)
	if handlerName == "" || paramName == "" {
		return
	}
	h, err := registry.GetHandler(handlerName)
	if err != nil {
		return
	}
	if field, ok := h.Params[paramName]; ok {
		e.Message = fmt.Sprintf("%s. Expected type: %s", e.Message, field.Type)
	}
}

// findClosestTopicMatch finds the most similar topic name to any word in the message.
// Returns empty string if no candidate is close enough.
func findClosestTopicMatch(message string, allTopics []string) string {
	if message == "" || len(allTopics) == 0 {
		return ""
	}

	// Try to find a word in the message that looks like a topic name (contains dots or underscores)
	words := strings.Fields(message)
	for _, word := range words {
		// Strip surrounding quotes and punctuation
		word = strings.Trim(word, `"'`+"`.,;:()")
		if strings.ContainsAny(word, "._") && len(word) > 3 {
			best := findClosestTopicString(word, allTopics)
			if best != "" {
				return best
			}
		}
	}
	return ""
}

// findClosestTopicString finds the closest topic name using Levenshtein distance.
func findClosestTopicString(target string, candidates []string) string {
	if len(candidates) == 0 || target == "" {
		return ""
	}

	bestMatch := ""
	bestDist := len(target)/2 + 1

	for _, candidate := range candidates {
		dist := levenshteinDist(strings.ToLower(target), strings.ToLower(candidate))
		if dist < bestDist {
			bestDist = dist
			bestMatch = candidate
		}
	}
	return bestMatch
}

// levenshteinDist computes edit distance between two strings.
func levenshteinDist(a, b string) int {
	la := len(a)
	lb := len(b)

	if la > lb {
		a, b = b, a
		la, lb = lb, la
	}

	prev := make([]int, la+1)
	curr := make([]int, la+1)

	for i := 0; i <= la; i++ {
		prev[i] = i
	}

	for j := 1; j <= lb; j++ {
		curr[0] = j
		for i := 1; i <= la; i++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			del := prev[i] + 1
			ins := curr[i-1] + 1
			sub := prev[i-1] + cost
			curr[i] = min3(del, ins, sub)
		}
		prev, curr = curr, prev
	}
	return prev[la]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}
