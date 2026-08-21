// Package ticket implements the twt Markdown ticket tracker. The ticket
// files are the store. Every mutation parses the YAML frontmatter into a
// yaml.Node tree, changes only the touched keys, and writes the file back, so
// unknown legacy keys, comments, and scalar styles survive every write.
package ticket

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/clierr"
	"go.yaml.in/yaml/v3"
)

// fence is one frontmatter fence line.
const fence = "---"

// TicketFile is one parsed ticket file. Doc is the frontmatter document node
// tree, or nil when the file has no frontmatter. Body holds every byte after
// the closing fence line, byte-exact.
type TicketFile struct {
	Path string
	Doc  *yaml.Node
	Body string
}

// ParseTicketFile splits raw into frontmatter and body. A file has
// frontmatter only when it starts with exactly "---\n". The closing fence is
// the next line that is exactly "---". The body keeps every byte after the
// newline of the closing fence.
func ParseTicketFile(path string, raw []byte) (*TicketFile, error) {
	head := raw
	if len(head) > 4096 {
		head = head[:4096]
	}
	if bytes.Contains(head, []byte("\r\n")) {
		return nil, clierr.WithHint(
			clierr.New(clierr.UnsafeState, "ticket file %q uses CRLF line endings", path),
			"Convert the file to LF line endings.")
	}
	if !bytes.HasPrefix(raw, []byte(fence+"\n")) {
		return &TicketFile{Path: path, Body: string(raw)}, nil
	}
	rest := raw[len(fence)+1:]
	offset := 0
	for offset <= len(rest) {
		lineEnd := bytes.IndexByte(rest[offset:], '\n')
		var line []byte
		if lineEnd < 0 {
			line = rest[offset:]
		} else {
			line = rest[offset : offset+lineEnd]
		}
		if string(line) == fence {
			frontmatter := rest[:offset]
			body := ""
			if lineEnd >= 0 {
				body = string(rest[offset+lineEnd+1:])
			}
			var doc yaml.Node
			if err := yaml.Unmarshal(frontmatter, &doc); err != nil {
				return nil, clierr.Wrap(clierr.UnsafeState,
					fmt.Errorf("ticket file %q has invalid frontmatter: %w", path, err))
			}
			if doc.Kind == 0 {
				// Empty frontmatter block. Keep a usable empty document.
				doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
			}
			return &TicketFile{Path: path, Doc: &doc, Body: body}, nil
		}
		if lineEnd < 0 {
			break
		}
		offset += lineEnd + 1
	}
	return nil, clierr.New(clierr.UnsafeState, "ticket file %q has an unterminated frontmatter fence", path)
}

// Mapping returns the top-level frontmatter mapping node, or nil when the
// file has no frontmatter mapping.
func (f *TicketFile) Mapping() *yaml.Node {
	if f == nil || f.Doc == nil || len(f.Doc.Content) == 0 {
		return nil
	}
	node := f.Doc.Content[0]
	if node.Kind != yaml.MappingNode {
		return nil
	}
	return node
}

// ensureMapping returns the frontmatter mapping node and creates an empty
// frontmatter document when the file has none.
func (f *TicketFile) ensureMapping() *yaml.Node {
	if mapping := f.Mapping(); mapping != nil {
		return mapping
	}
	mapping := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	f.Doc = &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{mapping}}
	return mapping
}

// Render writes the file back: fence, frontmatter with two-space indent,
// fence, then the body byte-exact.
func (f *TicketFile) Render() ([]byte, error) {
	var out bytes.Buffer
	out.WriteString(fence + "\n")
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(f.Doc); err != nil {
		encoder.Close()
		return nil, fmt.Errorf("encode ticket frontmatter: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, fmt.Errorf("encode ticket frontmatter: %w", err)
	}
	out.WriteString(fence + "\n")
	out.WriteString(f.Body)
	return out.Bytes(), nil
}

// findMapValue returns the value node of key inside mapping, or nil.
func findMapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// mapValueForUpdate returns the value node of key and appends a new pair when
// the key is absent. It keeps the key node and its comments in place.
func mapValueForUpdate(mapping *yaml.Node, key string) *yaml.Node {
	if value := findMapValue(mapping, key); value != nil {
		return value
	}
	value := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null"}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value)
	return value
}

// setMapString sets key to a string scalar. An existing string scalar keeps
// its style, so a quoted title stays quoted. An existing null becomes a plain
// string.
func setMapString(mapping *yaml.Node, key, value string) {
	node := mapValueForUpdate(mapping, key)
	style := yaml.Style(0)
	if node.Kind == yaml.ScalarNode && node.Tag == "!!str" {
		style = node.Style
	}
	node.Kind = yaml.ScalarNode
	node.Tag = "!!str"
	node.Value = value
	node.Style = style
	node.Content = nil
}

// setMapInt sets key to a plain integer scalar.
func setMapInt(mapping *yaml.Node, key string, value int) {
	node := mapValueForUpdate(mapping, key)
	node.Kind = yaml.ScalarNode
	node.Tag = "!!int"
	node.Value = fmt.Sprintf("%d", value)
	node.Style = 0
	node.Content = nil
}

// setMapDate sets key to a vault date scalar. The timestamp tag keeps the
// date plain and unquoted, exactly as Obsidian writes it.
func setMapDate(mapping *yaml.Node, key, value string) {
	node := mapValueForUpdate(mapping, key)
	node.Kind = yaml.ScalarNode
	node.Tag = "!!timestamp"
	node.Value = value
	node.Style = 0
	node.Content = nil
}

// setMapNull sets key to an empty null scalar, which renders as "key:".
func setMapNull(mapping *yaml.Node, key string) {
	node := mapValueForUpdate(mapping, key)
	node.Kind = yaml.ScalarNode
	node.Tag = "!!null"
	node.Value = ""
	node.Style = 0
	node.Content = nil
}

// setMapStringList sets key to a block sequence of plain strings. An empty
// slice renders as the flow sequence "[]".
func setMapStringList(mapping *yaml.Node, key string, values []string) {
	node := mapValueForUpdate(mapping, key)
	node.Kind = yaml.SequenceNode
	node.Tag = "!!seq"
	node.Value = ""
	node.Style = 0
	node.Content = nil
	if len(values) == 0 {
		node.Style = yaml.FlowStyle
		return
	}
	for _, value := range values {
		node.Content = append(node.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
}

// stripWikiLink turns "[[slug|display]]" or "[[slug#heading]]" into "slug".
// A value without wiki-link brackets only loses surrounding spaces.
func stripWikiLink(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]") {
		s = s[2 : len(s)-2]
	}
	if i := strings.IndexByte(s, '|'); i >= 0 {
		s = s[:i]
	}
	if i := strings.IndexByte(s, '#'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}
