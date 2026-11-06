// Package domain holds the entities and pure business rules of the PM assistant.
// Nothing in this package does I/O: no HTTP, no database, no clock reads.
package domain

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// StateGroup is the workflow bucket a task state belongs to.
type StateGroup string

const (
	GroupBacklog   StateGroup = "backlog"
	GroupUnstarted StateGroup = "unstarted"
	GroupStarted   StateGroup = "started"
	GroupCompleted StateGroup = "completed"
	GroupCancelled StateGroup = "cancelled"
)

type Project struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Identifier string `json:"identifier"`
}

type State struct {
	ID    string     `json:"id"`
	Name  string     `json:"name"`
	Group StateGroup `json:"group"`
}

type Member struct {
	ID          string `json:"id"`
	DisplayName string `json:"display_name"`
	FirstName   string `json:"first_name,omitempty"`
	LastName    string `json:"last_name,omitempty"`
	Email       string `json:"email,omitempty"`
}

// Name is how a member is shown to the owner.
func (m *Member) Name() string {
	if m == nil {
		return "?"
	}
	if m.DisplayName != "" {
		return m.DisplayName
	}
	full := strings.TrimSpace(strings.Join(nonEmpty(m.FirstName, m.LastName), " "))
	if full != "" {
		return full
	}
	if m.Email != "" {
		return m.Email
	}
	return "?"
}

type Label struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type Cycle struct {
	ID        string  `json:"id"`
	Name      string  `json:"name"`
	StartDate *string `json:"start_date"`
	EndDate   *string `json:"end_date"`
	Status    string  `json:"status,omitempty"`
}

// CycleWrite is the body of a new cycle (sprint). Dates are YYYY-MM-DD.
type CycleWrite struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	StartDate   string `json:"start_date"`
	EndDate     string `json:"end_date"`
}

type Module struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	StartDate  *string `json:"start_date"`
	TargetDate *string `json:"target_date"`
	Status     string  `json:"status,omitempty"`
}

// CycleLink records when a work item was put into a cycle.
type CycleLink struct {
	IssueID string `json:"issueId"`
	AddedAt string `json:"addedAt"`
}

// WorkItem is a task normalised from whichever tool provides it.
type WorkItem struct {
	ID          string   `json:"id"`
	ProjectID   string   `json:"projectId"`
	Key         string   `json:"key"`
	Name        string   `json:"name"`
	Priority    string   `json:"priority"`
	StateID     string   `json:"stateId"`
	AssigneeIDs []string `json:"assigneeIds"`
	LabelIDs    []string `json:"labelIds"`
	StartDate   *string  `json:"startDate"`
	TargetDate  *string  `json:"targetDate"`
	CompletedAt *string  `json:"completedAt"`
	CreatedAt   string   `json:"createdAt"`
	UpdatedAt   string   `json:"updatedAt"`
	ParentID    *string  `json:"parentId"`
}

// Target returns the target date or "" when unset.
func (w WorkItem) Target() string { return deref(w.TargetDate) }

// WorkItemWrite is the body of a create/update. Nil fields are left unchanged; a pointer to an
// empty assignee list clears the assignees.
type WorkItemWrite struct {
	Name            *string   `json:"name,omitempty"`
	DescriptionHTML *string   `json:"description_html,omitempty"`
	State           *string   `json:"state,omitempty"`
	Assignees       *[]string `json:"assignees,omitempty"`
	Priority        *string   `json:"priority,omitempty"`
	StartDate       *string   `json:"start_date,omitempty"`
	TargetDate      *string   `json:"target_date,omitempty"`
	Parent          *string   `json:"parent,omitempty"`
	Labels          *[]string `json:"labels,omitempty"`
}

var keyRe = regexp.MustCompile(`^([A-Z0-9]+)-(\d+)$`)

// SplitKey parses "FDABIGDATA-123" into its project identifier and sequence number.
func SplitKey(key string) (string, int, error) {
	m := keyRe.FindStringSubmatch(strings.ToUpper(strings.TrimSpace(key)))
	if m == nil {
		return "", 0, UserError(fmt.Sprintf(`รหัสงาน "%s" ไม่ถูกรูปแบบ (เช่น FDABIGDATA-123)`, key))
	}
	n, _ := strconv.Atoi(m[2])
	return m[1], n, nil
}

func nonEmpty(xs ...string) []string {
	out := xs[:0:0]
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// Ptr returns a pointer to v.
func Ptr[T any](v T) *T { return &v }
