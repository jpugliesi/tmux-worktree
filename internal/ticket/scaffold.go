package ticket

import (
	"embed"
	"path/filepath"
	"strings"
)

// scaffoldFS embeds the one-time scaffold notes: the vault hub index, the
// Project hub index, and the ticket create template. Init and CreateProject write
// each of them only when the target file is missing.
//
//go:embed scaffold/root-index.md scaffold/project-index.md scaffold/ticket.md
var scaffoldFS embed.FS

func scaffoldAsset(name string) string {
	data, err := scaffoldFS.ReadFile("scaffold/" + name)
	if err != nil {
		// The assets are compiled into the binary, so a miss is a build bug.
		panic(err)
	}
	return string(data)
}

// rootIndexContent renders the Tickets home index.md. The Bases block filters
// by the folder name of the Tickets home.
func rootIndexContent(home, created string) []byte {
	content := scaffoldAsset("root-index.md")
	content = strings.ReplaceAll(content, "{{FOLDER}}", filepath.Base(filepath.Clean(home)))
	content = strings.ReplaceAll(content, "{{CREATED}}", created)
	return []byte(content)
}

// projectIndexContent renders the index.md of one Project.
func projectIndexContent(name, created string) []byte {
	content := scaffoldAsset("project-index.md")
	content = strings.ReplaceAll(content, "{{TITLE}}", name)
	content = strings.ReplaceAll(content, "{{CREATED}}", created)
	return []byte(content)
}

// ticketTemplateContent returns the create template note.
func ticketTemplateContent() []byte {
	return []byte(scaffoldAsset("ticket.md"))
}
