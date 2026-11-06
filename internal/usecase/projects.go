package usecase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

var nonIdent = regexp.MustCompile(`[^A-Z0-9]`)

// projectIdent cleans an identifier, or makes one from the name's Latin letters and digits.
func projectIdent(ident, name string) string {
	src := ident
	if strings.TrimSpace(src) == "" {
		src = name
	}
	return domain.Truncate(nonIdent.ReplaceAllString(strings.ToUpper(src), ""), 12)
}

func roleOf(s string) (int, string) {
	switch domain.Norm(s) {
	case "admin", "แอดมิน", "ผู้ดูแล":
		return domain.RoleAdmin, "Admin"
	case "guest", "viewer", "ผู้ชม":
		return domain.RoleGuest, "Guest"
	}
	return domain.RoleMember, "Member"
}

// resolveNewMembers looks people up in the workspace; unknown names become warnings.
func (a *App) resolveNewMembers(ctx context.Context, people []string, role string, skip map[string]bool) ([]domain.PlanMember, []domain.PreviewLine, []string, error) {
	r, roleName := roleOf(role)
	members, lines, warnings := []domain.PlanMember{}, []domain.PreviewLine{}, []string{}
	seen := map[string]bool{}
	for _, who := range people {
		who = strings.TrimSpace(who)
		if who == "" {
			continue
		}
		m, err := a.ResolveWorkspaceMember(ctx, who)
		if err != nil {
			var u *domain.UserFacingError
			if errors.As(err, &u) {
				warnings = append(warnings, u.Msg)
				continue
			}
			return nil, nil, nil, err
		}
		if seen[m.ID] {
			continue
		}
		seen[m.ID] = true
		if skip[m.ID] {
			warnings = append(warnings, m.Name()+" อยู่ในโปรเจกต์แล้ว")
			continue
		}
		members = append(members, domain.PlanMember{ID: m.ID, Name: m.Name(), Role: r})
		lines = append(lines, domain.PreviewLine{Tag: "เพิ่ม", Text: m.Name(), Sub: joinNonEmpty(" · ", m.Email, roleName)})
	}
	return members, lines, warnings, nil
}

func (a *App) prepareProject(ctx context.Context, raw json.RawMessage) (domain.Preview, error) {
	var in struct {
		Name, Identifier, Description, Role string
		Members                             []string
	}
	if err := decode(raw, &in); err != nil {
		return domain.Preview{}, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		return domain.Preview{}, domain.UserError("ต้องมีชื่อโปรเจกต์")
	}
	ident := projectIdent(in.Identifier, in.Name)
	if ident == "" {
		return domain.Preview{}, domain.UserError("ต้องมีรหัสโปรเจกต์เป็นตัวอักษรภาษาอังกฤษหรือตัวเลข เช่น PEA3")
	}
	all, err := a.tasks().AllProjects(ctx)
	if err != nil {
		return domain.Preview{}, err
	}
	for _, p := range all {
		if strings.EqualFold(p.Identifier, ident) {
			return domain.Preview{}, domain.UserError(fmt.Sprintf("รหัส %s ถูกใช้แล้วกับโปรเจกต์ %s", ident, p.Name))
		}
		if strings.EqualFold(strings.TrimSpace(p.Name), in.Name) {
			return domain.Preview{}, domain.UserError(fmt.Sprintf(`มีโปรเจกต์ชื่อ "%s" อยู่แล้ว`, p.Name))
		}
	}
	me, _ := a.tasks().Me(ctx)
	members, mLines, warnings, err := a.resolveNewMembers(ctx, in.Members, in.Role, map[string]bool{me.ID: me.ID != ""})
	if err != nil {
		return domain.Preview{}, err
	}
	lines := []domain.PreviewLine{{Text: "รหัสงาน", Sub: ident + "-1, " + ident + "-2, …"}}
	if in.Description != "" {
		lines = append(lines, domain.PreviewLine{Text: "รายละเอียด", Sub: cut(in.Description, 200)})
	}
	lines = append(lines, mLines...)
	return domain.Preview{
		Title: "โปรเจกต์ใหม่: " + in.Name, Subtitle: fmt.Sprintf("คุณเป็น Admin · สมาชิกเพิ่ม %d คน", len(members)), Lines: lines, Warnings: warnings,
		ConfirmLabel: "ยืนยัน สร้างโปรเจกต์",
		Plan: domain.WritePlan{Kind: domain.PlanProject, Members: members,
			Project: &domain.ProjectWrite{Name: in.Name, Identifier: ident, Description: in.Description}},
	}, nil
}

func (a *App) prepareMembers(ctx context.Context, raw json.RawMessage) (domain.Preview, error) {
	var in struct {
		Project, Role string
		Members       []string
	}
	if err := decode(raw, &in); err != nil {
		return domain.Preview{}, err
	}
	p, err := a.ResolveProject(ctx, in.Project)
	if err != nil {
		return domain.Preview{}, err
	}
	current, err := a.tasks().Members(ctx, p.ID)
	if err != nil {
		return domain.Preview{}, err
	}
	skip := map[string]bool{}
	for _, m := range current {
		skip[m.ID] = true
	}
	members, lines, warnings, err := a.resolveNewMembers(ctx, in.Members, in.Role, skip)
	if err != nil {
		return domain.Preview{}, err
	}
	if len(members) == 0 {
		msg := "ไม่มีคนที่จะเพิ่ม"
		if len(warnings) > 0 {
			msg += ": " + strings.Join(warnings, " / ")
		}
		return domain.Preview{}, domain.UserError(msg)
	}
	return domain.Preview{
		Title: "เพิ่มคนเข้า " + p.Name, Subtitle: fmt.Sprintf("%d คน", len(members)), Lines: lines, Warnings: warnings,
		ConfirmLabel: fmt.Sprintf("ยืนยัน เพิ่ม %d คน", len(members)),
		Plan:         domain.WritePlan{Kind: domain.PlanMembers, ProjectID: &p.ID, Members: members},
	}, nil
}

// addMembers adds each person; failures (often an API that does not allow it) come back with the members page link.
func (a *App) addMembers(ctx context.Context, p domain.Project, members []domain.PlanMember) (added, failed []string, link string) {
	tp := a.tasks()
	for _, m := range members {
		if err := tp.AddProjectMember(ctx, p.ID, m.ID, m.Role); err != nil {
			failed = append(failed, m.Name)
			continue
		}
		added = append(added, m.Name)
	}
	if len(failed) > 0 {
		link = tp.MembersURL(p)
	}
	return added, failed, link
}

func (a *App) membersResult(p domain.Project, prefix string, added, failed []string, link string) Result {
	summary := prefix
	if len(added) > 0 {
		summary = joinNonEmpty(" · ", summary, "เพิ่ม "+strings.Join(added, ", "))
	}
	links := []domain.Link{}
	if u := a.tasks().ProjectURL(p); u != "" {
		links = append(links, domain.Link{Label: "เปิด " + p.Name, URL: u})
	}
	r := Result{"summary": summary, "added": added}
	if len(failed) > 0 {
		r["failed"] = failed
		r["note"] = "ระบบจัดการงานไม่ยอมให้เพิ่มสมาชิกผ่าน API ให้ผู้ใช้กดเพิ่มเองที่หน้า Members"
		if link != "" {
			links = append(links, domain.Link{Label: "เพิ่มสมาชิกเอง (" + strings.Join(failed, ", ") + ")", URL: link})
		}
	}
	r["links"] = links
	return r
}

func (a *App) executeProject(ctx context.Context, plan domain.WritePlan) (Result, error) {
	switch plan.Kind {
	case domain.PlanProject:
		if plan.Project == nil {
			return nil, fmt.Errorf("project plan without project")
		}
		p, err := a.tasks().NewProject(ctx, *plan.Project)
		if err != nil {
			return nil, err
		}
		if p.Identifier == "" {
			p.Identifier = plan.Project.Identifier
		}
		a.followProject(p.Identifier)
		added, failed, link := a.addMembers(ctx, p, plan.Members)
		return a.membersResult(p, "สร้างโปรเจกต์ "+p.Name, added, failed, link), nil
	case domain.PlanMembers:
		p, err := a.findProject(ctx, deref(plan.ProjectID))
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, &domain.StatusError{Status: 404, Msg: "project not found"}
		}
		added, failed, link := a.addMembers(ctx, *p, plan.Members)
		if len(added) == 0 && len(failed) > 0 {
			r := a.membersResult(*p, "", added, failed, link)
			r["summary"] = "เพิ่มสมาชิกผ่าน API ไม่ได้"
			return r, nil
		}
		return a.membersResult(*p, "", added, failed, link), nil
	}
	return nil, fmt.Errorf("unknown plan %s", plan.Kind)
}

// followProject keeps a new project visible when the owner follows a chosen list.
func (a *App) followProject(ident string) {
	t := a.Store.Connections().Task
	var list []string
	switch {
	case t.Provider == domain.ProviderBuiltin:
		return
	case t.Plane != nil:
		list = t.Plane.Projects
	default:
		list = a.Cfg.PlaneProjects
	}
	if len(list) == 0 {
		return // following everything already
	}
	for _, x := range list {
		if strings.EqualFold(x, ident) {
			return
		}
	}
	a.SetFollowed(append(append([]string{}, list...), ident))
}
