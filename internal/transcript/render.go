package transcript

import (
	"fmt"
	"strings"
	"time"
)

func makeTranscript(provider, sessionID, repository string, updatedAt time.Time, events []event) (Transcript, error) {
	parts := []string{}
	lastRole := ""
	for _, item := range events {
		text := strings.TrimSpace(item.text)
		if text == "" {
			continue
		}
		if item.role != lastRole {
			if len(parts) > 0 {
				parts = append(parts, "")
			}
			heading := "## Assistant"
			if item.role == "user" {
				heading = "## User"
			}
			parts = append(parts, heading)
			lastRole = item.role
		}
		parts = append(parts, text)
	}
	if len(parts) == 0 {
		return Transcript{}, fmt.Errorf("%s transcript %q has no user-visible text", providerName(provider), sessionID)
	}
	return Transcript{
		Provider: provider, SessionID: sessionID, RepositoryName: repository,
		UpdatedAt: updatedAt.UTC(), Markdown: strings.Join(parts, "\n\n") + "\n",
	}, nil
}

func contentText(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		parts := []string{}
		for _, item := range typed {
			if text := strings.TrimSpace(contentText(item)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	case map[string]any:
		if text := stringValue(typed["text"]); text != "" {
			return text
		}
		return stringValue(typed["output_text"])
	default:
		return ""
	}
}

func mapValue(value any) map[string]any {
	if result, ok := value.(map[string]any); ok {
		return result
	}
	return map[string]any{}
}

func stringValue(value any) string {
	result, _ := value.(string)
	return result
}
