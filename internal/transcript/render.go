package transcript

import (
	"strings"
	"time"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
)

func makeTranscript(provider, sessionID, repository string, updatedAt time.Time, events []event) (Transcript, error) {
	parts := []string{}
	lastRole := ""
	for _, item := range events {
		text := strings.TrimSpace(sanitizeUntrusted(item.text))
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
		return Transcript{}, clierr.New(clierr.PreconditionFailed, "%s transcript %q has no user-visible text", providerName(provider), sessionID)
	}
	return Transcript{
		Provider: provider, SessionID: sessionID, RepositoryName: repository,
		UpdatedAt: updatedAt.UTC(), Markdown: strings.Join(parts, "\n\n") + "\n",
	}, nil
}

// sanitizeUntrusted removes terminal control text from untrusted transcript
// text. A provider transcript can carry escape sequences that hide or rewrite
// what a reader sees, and every twt transcript output goes into a person or
// agent context. The result keeps all printable text, which includes emoji,
// and keeps line feeds and tabs.
//
// It removes:
//   - a CSI sequence, such as the color sequence "\x1b[31m"
//   - an OSC string, such as an OSC 8 hyperlink; the link text stays
//   - a DCS, SOS, PM, or APC string, and every other ESC sequence
//   - a C0 control character, except a line feed and a tab
//   - a DEL character and a C1 control character, which some terminals read
//     as an escape
//
// It writes one line feed for a carriage return, so "\r\n" and a bare "\r"
// both become one line feed.
func sanitizeUntrusted(text string) string {
	runes := []rune(text)
	var builder strings.Builder
	builder.Grow(len(text))
	for index := 0; index < len(runes); index++ {
		character := runes[index]
		switch {
		case character == 0x1b:
			index = skipEscape(runes, index)
		case character == '\r':
			if index+1 < len(runes) && runes[index+1] == '\n' {
				continue
			}
			builder.WriteRune('\n')
		case character == '\n' || character == '\t':
			builder.WriteRune(character)
		case character < 0x20 || character == 0x7f:
		case character >= 0x80 && character <= 0x9f:
		default:
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

// SanitizeUntrusted removes terminal control text from untrusted display text.
// Agent Transcript rendering and live-pane Agent Previews use the same rules.
func SanitizeUntrusted(text string) string { return sanitizeUntrusted(text) }

// skipEscape returns the index of the last rune of the escape sequence that
// starts at start. A sequence with no end consumes the rest of the text.
func skipEscape(runes []rune, start int) int {
	next := start + 1
	if next >= len(runes) {
		return start
	}
	switch runes[next] {
	case '[':
		// A CSI sequence ends at its final byte, from "@" to "~".
		for index := next + 1; index < len(runes); index++ {
			if runes[index] >= '@' && runes[index] <= '~' {
				return index
			}
		}
		return len(runes) - 1
	case ']', 'P', 'X', '^', '_':
		// An OSC, DCS, SOS, PM, or APC string ends at BEL or at ST.
		for index := next + 1; index < len(runes); index++ {
			if runes[index] == 0x07 {
				return index
			}
			if runes[index] == 0x1b && index+1 < len(runes) && runes[index+1] == '\\' {
				return index + 1
			}
		}
		return len(runes) - 1
	default:
		return next
	}
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
