package tmux

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jpugliesi/tmux-worktree/internal/domain"
)

const paneFieldSeparator = "\x1f"

// ProcessObservation is the stable provider-relevant evidence for one
// operating-system process at the time of one process-table snapshot.
type ProcessObservation struct {
	ID                int
	ParentID          int
	GroupID           int
	ForegroundGroupID int
	TTY               string
	Started           string
	Command           string
	Executable        string
	Args              []string
}

// PaneObservation is one pane of an owned Workspace and the processes in its
// current foreground process group. It is read-only evidence, not identity.
type PaneObservation struct {
	ID             string
	RootProcessID  int
	RootStarted    string
	TTY            string
	Dead           bool
	CurrentCommand string
	StartCommand   string
	CurrentPath    string
	AgentID        string
	Foreground     []ProcessObservation
}

// ProcessBinding is the process identity that twt saved when it adopted a
// provider process below a shell-hosted pane.
type ProcessBinding struct {
	PaneRootID      int
	PaneRootStarted string
	ID              int
	Started         string
	Command         string
	Evidence        string
	ReadyCommand    string
}

// ExplainProcessPane returns the checks for one saved process binding. The
// current command is advisory for focus and required for sending input.
func (c Client) ExplainProcessPane(workspace domain.Workspace, paneID, agentID string, binding ProcessBinding, requireReady bool) []PaneCheck {
	panes, err := c.ObserveWorkspace(workspace)
	workspacePane := false
	if err != nil {
		panes = nil
	}
	marked, rootMatches, live, ready := false, false, false, false
	for _, pane := range panes {
		if pane.ID != paneID {
			continue
		}
		workspacePane = true
		marked = pane.AgentID == agentID
		rootMatches = pane.RootProcessID == binding.PaneRootID && pane.RootStarted == binding.PaneRootStarted
		for _, process := range pane.Foreground {
			if !processMatchesBinding(process, binding) {
				continue
			}
			live = !pane.Dead
			readyCommand := binding.ReadyCommand
			if readyCommand == "" {
				readyCommand = process.Command
			}
			ready = live && sameProcessCommand(pane.CurrentCommand, readyCommand)
			break
		}
		break
	}
	return []PaneCheck{
		{Name: "workspace pane", OK: workspacePane && paneID != ""},
		{Name: "agent marker", OK: marked},
		{Name: "saved pane process", OK: rootMatches},
		{Name: "saved provider process", OK: live},
		{Name: "provider accepts input", OK: ready, Advisory: !requireReady},
	}
}

func processMatchesBinding(process ProcessObservation, binding ProcessBinding) bool {
	return process.ID == binding.ID && process.Started == binding.Started &&
		sameProcessCommand(process.Command, binding.Command) && ProcessEvidence(process) == binding.Evidence
}

// ProcessEvidence returns a one-way digest of provider-identifying process
// data. Agent Session state stores this digest, not process arguments.
func ProcessEvidence(process ProcessObservation) string {
	value := strings.Join(append([]string{process.Command, process.Executable}, process.Args...), "\x00")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

// ProcessPaneBelongs reports whether a saved process still owns the pane.
func (c Client) ProcessPaneBelongs(workspace domain.Workspace, paneID, agentID string, binding ProcessBinding, requireReady bool) bool {
	for _, check := range c.ExplainProcessPane(workspace, paneID, agentID, binding, requireReady) {
		if !check.Advisory && !check.OK {
			return false
		}
	}
	return true
}

// FocusProcess focuses a pane after a fresh check of its saved process.
func (c Client) FocusProcess(workspace domain.Workspace, paneID, agentID string, binding ProcessBinding) error {
	if !c.ProcessPaneBelongs(workspace, paneID, agentID, binding, false) {
		return NotLiveError(agentID)
	}
	if _, err := c.output(nil, "select-window", "-t", paneID); err != nil {
		return fmt.Errorf("select Agent Session window: %w", err)
	}
	if _, err := c.output(nil, "select-pane", "-t", paneID); err != nil {
		return fmt.Errorf("focus Agent Session: %w", err)
	}
	return nil
}

// SendProcess sends text only when the saved provider process is still the
// pane's foreground command.
func (c Client) SendProcess(workspace domain.Workspace, paneID, agentID string, binding ProcessBinding, text string) error {
	if !c.ProcessPaneBelongs(workspace, paneID, agentID, binding, true) {
		return NotLiveError(agentID)
	}
	return c.sendChecked(paneID, text, func() error {
		if !c.ProcessPaneBelongs(workspace, paneID, agentID, binding, true) {
			return NotLiveError(agentID)
		}
		return nil
	})
}

func sameProcessCommand(first, second string) bool {
	return strings.EqualFold(filepath.Base(strings.TrimSpace(first)), filepath.Base(strings.TrimSpace(second)))
}

// ObserveWorkspace reads the pane inventory once and the process table once.
// It returns only panes from the tmux session that the Workspace owns.
func (c Client) ObserveWorkspace(workspace domain.Workspace) ([]PaneObservation, error) {
	sessionID, err := c.workspaceSession(workspace)
	if err != nil {
		return nil, err
	}
	format := strings.Join([]string{
		"#{pane_id}", "#{pane_pid}", "#{pane_tty}", "#{pane_dead}",
		"#{pane_current_command}", "#{pane_start_command}", "#{pane_current_path}", "#{@twt_agent_id}", "",
	}, paneFieldSeparator)
	rows, err := c.output(nil, "list-panes", "-s", "-t", sessionID, "-F", format)
	if err != nil {
		return nil, fmt.Errorf("list Workspace panes: %w", err)
	}
	processRows, err := c.processOutput()
	if err != nil {
		return nil, fmt.Errorf("read process table: %w", err)
	}
	processes := parseProcesses(processRows)
	panes := parsePanes(rows)
	processByID := make(map[int]ProcessObservation, len(processes))
	for _, process := range processes {
		processByID[process.ID] = process
	}
	for index := range panes {
		panes[index].RootStarted = processByID[panes[index].RootProcessID].Started
		panes[index].Foreground = foregroundDescendants(panes[index], processes)
	}
	return panes, nil
}

func parsePanes(rows string) []PaneObservation {
	// A pane row is 8 fields plus one trailing separator. Rows cannot split
	// on newlines: pane_start_command carries a full agent start command,
	// which can contain a multi-line prompt. Parse the whole output as one
	// separator-delimited token stream instead; tmux joins rows with "\n",
	// which lands at the front of the next row's first token.
	tokens := strings.Split(rows, paneFieldSeparator)
	panes := []PaneObservation{}
	for start := 0; start+8 < len(tokens); start += 8 {
		fields := tokens[start : start+8]
		id := strings.TrimPrefix(fields[0], "\n")
		pid, pidErr := strconv.Atoi(fields[1])
		dead, deadErr := strconv.Atoi(fields[3])
		if pidErr != nil || deadErr != nil || id == "" {
			continue
		}
		panes = append(panes, PaneObservation{
			ID: id, RootProcessID: pid, TTY: normalizeTTY(fields[2]), Dead: dead != 0,
			CurrentCommand: fields[4], StartCommand: fields[5], CurrentPath: fields[6], AgentID: fields[7],
		})
	}
	return panes
}

func (c Client) processOutput() (string, error) {
	if c.runProcesses != nil {
		return c.runProcesses()
	}
	command := exec.Command("ps", "-A", "-ww", "-o", "pid=,ppid=,pgid=,tpgid=,tty=,lstart=,comm=,args=")
	command.Env = append(os.Environ(), "LC_ALL=C")
	output, err := command.Output()
	return string(output), err
}

func parseProcesses(rows string) []ProcessObservation {
	processes := []ProcessObservation{}
	for _, row := range strings.Split(rows, "\n") {
		fields := strings.Fields(row)
		if len(fields) < 12 {
			continue
		}
		ids := make([]int, 4)
		valid := true
		for index := range ids {
			value, err := strconv.Atoi(fields[index])
			if err != nil {
				valid = false
				break
			}
			ids[index] = value
		}
		if !valid || ids[0] <= 0 {
			continue
		}
		args := append([]string(nil), fields[11:]...)
		processes = append(processes, ProcessObservation{
			ID: ids[0], ParentID: ids[1], GroupID: ids[2], ForegroundGroupID: ids[3],
			TTY: normalizeTTY(fields[4]), Started: strings.Join(fields[5:10], " "),
			Command: filepath.Base(fields[10]), Executable: processExecutable(args), Args: args,
		})
	}
	return processes
}

func processExecutable(args []string) string {
	if len(args) == 0 || !filepath.IsAbs(args[0]) {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(args[0])
	if err != nil {
		return ""
	}
	return resolved
}

func foregroundDescendants(pane PaneObservation, processes []ProcessObservation) []ProcessObservation {
	byID := make(map[int]ProcessObservation, len(processes))
	for _, process := range processes {
		byID[process.ID] = process
	}
	root, found := byID[pane.RootProcessID]
	if !found || root.ForegroundGroupID <= 0 || pane.Dead {
		return nil
	}
	foreground := []ProcessObservation{}
	for _, process := range processes {
		if process.GroupID != root.ForegroundGroupID || normalizeTTY(process.TTY) != normalizeTTY(pane.TTY) {
			continue
		}
		if process.ID == pane.RootProcessID || isDescendant(process, pane.RootProcessID, byID) {
			foreground = append(foreground, process)
		}
	}
	sort.Slice(foreground, func(i, j int) bool { return foreground[i].ID < foreground[j].ID })
	return foreground
}

func isDescendant(process ProcessObservation, ancestorID int, processes map[int]ProcessObservation) bool {
	seen := map[int]bool{}
	for process.ParentID > 0 && !seen[process.ParentID] {
		if process.ParentID == ancestorID {
			return true
		}
		seen[process.ParentID] = true
		parent, found := processes[process.ParentID]
		if !found {
			return false
		}
		process = parent
	}
	return false
}

func normalizeTTY(value string) string {
	value = strings.TrimSpace(value)
	if value == "?" || value == "??" || value == "-" {
		return ""
	}
	return filepath.Base(value)
}
