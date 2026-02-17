package qmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func parseSearchResults(input []byte) []SearchResult {
	trimmed := strings.TrimSpace(string(input))
	if trimmed == "" {
		return nil
	}

	var root any
	if err := json.Unmarshal([]byte(trimmed), &root); err != nil {
		return nil
	}
	items := extractSearchItems(root)
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		record, ok := item.(map[string]any)
		if !ok {
			continue
		}
		path := nonEmptyString(record["path"], record["filepath"], record["file"])
		docID := nonEmptyString(record["docid"], record["doc_id"], record["id"])
		snippet := nonEmptyString(record["snippet"], record["text"], record["content"], record["title"])
		if strings.TrimSpace(path) == "" && strings.TrimSpace(docID) == "" {
			continue
		}
		results = append(results, SearchResult{
			Path:    strings.TrimSpace(path),
			DocID:   strings.TrimSpace(docID),
			Score:   toFloat(record["score"]),
			Snippet: strings.TrimSpace(snippet),
		})
	}
	return results
}

func extractSearchItems(root any) []any {
	switch value := root.(type) {
	case []any:
		return value
	case map[string]any:
		for _, key := range []string{"results", "items", "data"} {
			if raw, ok := value[key]; ok {
				if list, ok := raw.([]any); ok {
					return list
				}
			}
		}
	}
	return nil
}

func nonEmptyString(values ...any) string {
	for _, value := range values {
		text := strings.TrimSpace(fmt.Sprintf("%v", value))
		if text == "" || text == "<nil>" {
			continue
		}
		return text
	}
	return ""
}

func toFloat(value any) float64 {
	switch number := value.(type) {
	case float64:
		return number
	case float32:
		return float64(number)
	case int:
		return float64(number)
	case int64:
		return float64(number)
	case json.Number:
		parsed, err := number.Float64()
		if err == nil {
			return parsed
		}
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(number), 64)
		if err == nil {
			return parsed
		}
	}
	return 0
}
