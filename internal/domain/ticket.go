package domain

import (
	"fmt"
	"regexp"
	"strings"
)

// TicketStatus is the triage state of one Ticket.
type TicketStatus string

const (
	TicketNeedsTriage   TicketStatus = "needs-triage"
	TicketNeedsInfo     TicketStatus = "needs-info"
	TicketReadyForAgent TicketStatus = "ready-for-agent"
	TicketReadyForHuman TicketStatus = "ready-for-human"
	TicketWontfix       TicketStatus = "wontfix"
	TicketDone          TicketStatus = "done"
)

// TicketStatuses lists every valid status in sorted order for schema output
// and error hints.
func TicketStatuses() []string {
	return []string{
		string(TicketDone),
		string(TicketNeedsInfo),
		string(TicketNeedsTriage),
		string(TicketReadyForAgent),
		string(TicketReadyForHuman),
		string(TicketWontfix),
	}
}

// ValidTicketStatus reports whether status is one of the six known statuses.
func ValidTicketStatus(status TicketStatus) bool {
	switch status {
	case TicketNeedsTriage, TicketNeedsInfo, TicketReadyForAgent,
		TicketReadyForHuman, TicketWontfix, TicketDone:
		return true
	}
	return false
}

var ticketSlugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// TicketSlugMaxLength caps the length of a Ticket slug.
const TicketSlugMaxLength = 60

// Ticket is one Markdown ticket file. The yaml tags name the frontmatter
// keys. The json tags follow the twt JSON envelope.
type Ticket struct {
	Slug      string       `yaml:"-" json:"slug"`
	Title     string       `yaml:"title" json:"title"`
	Aliases   []string     `yaml:"aliases" json:"-"`
	Status    TicketStatus `yaml:"status" json:"status"`
	Priority  int          `yaml:"priority" json:"priority"`
	Project   string       `yaml:"project" json:"project"`
	BlockedBy []string     `yaml:"blocked_by" json:"blockedBy"`
	ClaimedBy string       `yaml:"claimed_by" json:"claimedBy"`
	ClaimedAt string       `yaml:"claimed_at" json:"-"`
	Created   string       `yaml:"created" json:"created"`
	Updated   string       `yaml:"updated" json:"updated"`
	Path      string       `yaml:"-" json:"path"`
}

// Project is one directory of Tickets under the Tickets home.
type Project struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	Tickets  int    `json:"tickets"`
	HasIndex bool   `json:"hasIndex"`
}

// Validate checks a Ticket before a write. Reads stay tolerant; this check
// guards new content only.
func (t Ticket) Validate() error {
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("ticket %q has no title", t.Slug)
	}
	if !ValidTicketStatus(t.Status) {
		return fmt.Errorf("ticket status %q is not one of %s", t.Status, strings.Join(TicketStatuses(), ", "))
	}
	if t.Priority < 0 || t.Priority > 4 {
		return fmt.Errorf("ticket priority %d is not in the range 0 to 4", t.Priority)
	}
	if len(t.Slug) > TicketSlugMaxLength {
		return fmt.Errorf("ticket slug %q is longer than %d characters", t.Slug, TicketSlugMaxLength)
	}
	if !ticketSlugPattern.MatchString(t.Slug) {
		return fmt.Errorf("ticket slug %q is invalid: use lowercase letters, digits, and hyphens", t.Slug)
	}
	return nil
}

// Slugify derives a kebab slug from a title. It lowercases ASCII letters,
// turns each run of other ASCII characters into one hyphen, strips non-ASCII
// characters, trims hyphens, and caps the result at TicketSlugMaxLength. A
// cap cuts at the last hyphen before the limit when one exists. The result
// can be empty.
func Slugify(title string) string {
	var out strings.Builder
	pendingHyphen := false
	for _, r := range title {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			if pendingHyphen && out.Len() > 0 {
				out.WriteByte('-')
			}
			pendingHyphen = false
			out.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			if pendingHyphen && out.Len() > 0 {
				out.WriteByte('-')
			}
			pendingHyphen = false
			out.WriteRune(r + ('a' - 'A'))
		case r < 128:
			pendingHyphen = true
		}
		// Non-ASCII runes are stripped and add no hyphen.
	}
	slug := out.String()
	if len(slug) > TicketSlugMaxLength {
		cut := slug[:TicketSlugMaxLength]
		if i := strings.LastIndexByte(cut, '-'); i > 0 {
			cut = cut[:i]
		}
		slug = cut
	}
	return strings.Trim(slug, "-")
}
