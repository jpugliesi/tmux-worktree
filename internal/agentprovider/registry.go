// Package agentprovider is the authority for Agent Session provider names and
// provider capabilities that do not depend on a Workspace or on twt state.
package agentprovider

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// Process is the provider-relevant identity of one operating-system process.
// Executable is a resolved executable path when the runtime can prove one.
type Process struct {
	Command    string
	Executable string
	Args       []string
}

// Descriptor holds the stable capabilities of one Agent Session provider.
// Provider-specific matching rules stay private to this package.
type Descriptor struct {
	name       string
	aliases    []string
	transcript bool
	resume     []string
	match      func(Process) bool
}

var descriptors = []Descriptor{
	{name: "codex", aliases: []string{"codex"}, transcript: true, resume: []string{"codex", "resume", sessionToken}},
	{name: "claude", aliases: []string{"claude"}, transcript: true, resume: []string{"claude", "--resume", sessionToken}},
	{name: "cursor", aliases: []string{"cursor-agent"}, match: matchesCursorProcess},
	{name: "grok", aliases: []string{"grok"}, transcript: true, resume: []string{"grok", "--resume", sessionToken}},
	{name: "command"},
}

const sessionToken = "{session}"

// Names returns provider names in their stable display and schema order.
func Names() []string {
	names := make([]string, 0, len(descriptors))
	for _, descriptor := range descriptors {
		names = append(names, descriptor.name)
	}
	return names
}

// Lookup returns one provider descriptor by its exact canonical name.
func Lookup(name string) (Descriptor, bool) {
	for _, descriptor := range descriptors {
		if descriptor.name == name {
			return descriptor, true
		}
	}
	return Descriptor{}, false
}

// Name returns the canonical provider name.
func (d Descriptor) Name() string { return d.name }

// SupportsTranscript reports whether the provider has an Adapter that can
// prove an Agent Transcript belongs to an exact Workspace.
func (d Descriptor) SupportsTranscript() bool { return d.transcript }

// ResumeCommand returns the provider command for a verified session ID.
func (d Descriptor) ResumeCommand(sessionID string) []string {
	if sessionID == "" || len(d.resume) == 0 {
		return nil
	}
	command := append([]string(nil), d.resume...)
	for index, argument := range command {
		if argument == sessionToken {
			command[index] = sessionID
		}
	}
	return command
}

// IdentifyCommand finds the provider of an explicit start or resume command.
// Unknown non-shell commands use the manual command provider.
func IdentifyCommand(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	name := commandBase(argv[0])
	for _, descriptor := range descriptors {
		if descriptor.matchesAlias(name) {
			return descriptor.name
		}
	}
	if name == "agent" {
		executable := resolvedExecutable(argv[0])
		if matchesCursorProcess(Process{Command: name, Executable: executable, Args: argv}) {
			return "cursor"
		}
	}
	if isShell(name) {
		return ""
	}
	return "command"
}

// IdentifyProcess finds a known automatic provider from strong process
// evidence. The manual command provider is never discovered automatically.
func IdentifyProcess(process Process) string {
	argumentName := ""
	if len(process.Args) > 0 {
		argumentName = commandBase(process.Args[0])
	}
	for _, descriptor := range descriptors {
		if descriptor.name == "command" {
			continue
		}
		if descriptor.matchesAlias(argumentName) || descriptor.match != nil && descriptor.match(process) {
			return descriptor.name
		}
	}
	return ""
}

func (d Descriptor) matchesAlias(command string) bool {
	for _, alias := range d.aliases {
		if command == alias {
			return true
		}
	}
	return false
}

// Validate checks the static registry for ambiguous names and exact aliases.
func Validate() error {
	names := map[string]bool{}
	aliases := map[string]string{}
	for _, descriptor := range descriptors {
		if descriptor.name == "" || names[descriptor.name] {
			return fmt.Errorf("duplicate or empty Agent Session provider %q", descriptor.name)
		}
		names[descriptor.name] = true
		for _, alias := range descriptor.aliases {
			if owner := aliases[alias]; owner != "" {
				return fmt.Errorf("Agent Session executable alias %q belongs to both %q and %q", alias, owner, descriptor.name)
			}
			aliases[alias] = descriptor.name
		}
	}
	return nil
}

func matchesCursorProcess(process Process) bool {
	if len(process.Args) == 0 {
		return false
	}
	if commandBase(process.Args[0]) == "cursor-agent" {
		return true
	}
	if commandBase(process.Args[0]) != "agent" || !isCursorVersionExecutable(process.Executable) {
		return false
	}
	wantScript := canonicalPath(filepath.Join(filepath.Dir(filepath.Clean(process.Executable)), "index.js"))
	for _, argument := range process.Args[1:] {
		if filepath.IsAbs(argument) && canonicalPath(argument) == wantScript {
			return true
		}
	}
	return false
}

func canonicalPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return filepath.Clean(resolved)
	}
	return filepath.Clean(path)
}

func isCursorVersionExecutable(path string) bool {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) || filepath.Base(path) != "cursor-agent" {
		return false
	}
	versionDirectory := filepath.Dir(path)
	versionsDirectory := filepath.Dir(versionDirectory)
	return filepath.Base(versionDirectory) != "" && filepath.Base(versionsDirectory) == "versions" &&
		filepath.Base(filepath.Dir(versionsDirectory)) == "cursor-agent"
}

func resolvedExecutable(command string) string {
	path := command
	if !filepath.IsAbs(path) {
		found, err := exec.LookPath(command)
		if err != nil {
			return ""
		}
		path = found
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	return resolved
}

func commandBase(command string) string {
	return strings.ToLower(filepath.Base(strings.Trim(command, "'\"")))
}

func isShell(command string) bool {
	switch command {
	case "sh", "bash", "zsh", "fish", "dash", "ksh", "csh", "tcsh":
		return true
	default:
		return false
	}
}
