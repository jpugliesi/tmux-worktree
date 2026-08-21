package transcript

import "testing"

func TestSanitizeUntrustedRemovesTerminalControlText(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "plain text", text: "twt agents list", want: "twt agents list"},
		{name: "color sequence", text: "\x1b[31mred\x1b[0m text", want: "red text"},
		{name: "cursor move and erase line", text: "first\x1b[2K\x1b[1;1Hsecond", want: "firstsecond"},
		{name: "osc 8 hyperlink keeps the link text", text: "see \x1b]8;;https://example.com\x07the docs\x1b]8;;\x07 now", want: "see the docs now"},
		{name: "osc window title with string terminator", text: "a\x1b]0;evil title\x1b\\b", want: "ab"},
		{name: "device control string", text: "a\x1bPq data \x1b\\b", want: "ab"},
		{name: "two character escape sequence", text: "a\x1bcb", want: "ab"},
		{name: "trailing escape", text: "done\x1b", want: "done"},
		{name: "unfinished csi consumes the rest", text: "keep\x1b[31", want: "keep"},
		{name: "carriage return and line feed", text: "one\r\ntwo\r\n", want: "one\ntwo\n"},
		{name: "bare carriage return", text: "progress\rfinal", want: "progress\nfinal"},
		{name: "tab and line feed stay", text: "a\tb\nc", want: "a\tb\nc"},
		{name: "c0 control characters go", text: "a\x00b\x07c\x08d", want: "abcd"},
		{name: "c1 control character goes", text: "a\u009bb", want: "ab"},
		{name: "delete character goes", text: "a\x7fbc", want: "abc"},
		{name: "emoji and accents stay", text: "ready 🚀 café ✅", want: "ready 🚀 café ✅"},
		{name: "markdown stays", text: "## User\n\n- run `twt doctor`\n", want: "## User\n\n- run `twt doctor`\n"},
		{name: "empty text", text: "", want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := sanitizeUntrusted(test.text); got != test.want {
				t.Fatalf("sanitizeUntrusted(%q) = %q, want %q", test.text, got, test.want)
			}
		})
	}
}
