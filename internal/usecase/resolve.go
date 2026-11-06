package usecase

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

// Turning what the owner typed ("FDA", "ต้น", "Blocked") into IDs of the connected task tool.

const needProjectPrefix = "NEED_PROJECT"

func (a *App) ResolveProject(ctx context.Context, ref string) (domain.Project, error) {
	projects, err := a.tasks().Projects(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	if len(projects) == 0 {
		return domain.Project{}, domain.UserError("ยังไม่มีโปรเจกต์ให้ใช้ (ยังไม่ได้เชื่อมระบบจัดการงาน หรือยังไม่ได้เลือกโปรเจกต์ที่ติดตามในหน้าตั้งค่า)")
	}
	if ref == "" {
		cur := a.Store.CurrentProject()
		for _, p := range projects {
			if p.ID == cur {
				return p, nil
			}
		}
		if len(projects) == 1 {
			return projects[0], nil
		}
		names := make([]string, len(projects))
		for i, p := range projects {
			names[i] = p.Name
		}
		return domain.Project{}, domain.UserError(needProjectPrefix + ": ยังไม่ได้เลือกโปรเจกต์ปัจจุบัน ให้ถามผู้ใช้ว่าหมายถึงโปรเจกต์ไหน: " + strings.Join(names, ", "))
	}
	q := domain.Norm(ref)
	for _, p := range projects {
		if domain.Norm(p.Identifier) == q {
			return p, nil
		}
	}
	for _, p := range projects {
		if domain.Norm(p.Name) == q {
			return p, nil
		}
	}
	for _, p := range projects {
		n, id := domain.Norm(p.Name), domain.Norm(p.Identifier)
		if strings.Contains(n, q) || strings.Contains(q, id) || strings.HasPrefix(id, q) {
			return p, nil
		}
	}
	all := make([]string, len(projects))
	for i, p := range projects {
		all[i] = fmt.Sprintf("%s (%s)", p.Name, p.Identifier)
	}
	return domain.Project{}, domain.UserError(fmt.Sprintf(`ไม่พบโปรเจกต์ "%s" ที่มี: %s`, ref, strings.Join(all, ", ")))
}

// ProjectOrNil resolves a project, returning nil instead of an error.
func (a *App) ProjectOrNil(ctx context.Context, ref string) *domain.Project {
	p, err := a.ResolveProject(ctx, ref)
	if err != nil {
		return nil
	}
	return &p
}

func (a *App) ProjectOfKey(ctx context.Context, key string) (domain.Project, error) {
	ident, _, err := domain.SplitKey(key)
	if err != nil {
		return domain.Project{}, err
	}
	ps, err := a.tasks().Projects(ctx)
	if err != nil {
		return domain.Project{}, err
	}
	for _, p := range ps {
		if strings.ToUpper(p.Identifier) == ident {
			return p, nil
		}
	}
	return domain.Project{}, domain.UserError("ไม่พบโปรเจกต์ของรหัส " + key)
}

// ResolveMember finds a member; who = "me" is the connected account, "none" returns nil.
func (a *App) ResolveMember(ctx context.Context, projectID, who string) (*domain.Member, error) {
	if who == "none" {
		return nil, nil
	}
	t := a.tasks()
	members, err := t.Members(ctx, projectID)
	if err != nil {
		return nil, err
	}
	if who == "me" {
		me, err := t.Me(ctx)
		if err != nil {
			return nil, err
		}
		for i := range members {
			if members[i].ID == me.ID {
				return &members[i], nil
			}
		}
		return &me, nil
	}
	if m := a.matchMember(members, who); m != nil {
		return m, nil
	}
	names := make([]string, len(members))
	for i := range members {
		names[i] = members[i].Name()
	}
	return nil, domain.UserError(fmt.Sprintf(`ไม่พบสมาชิกชื่อ "%s" ในโปรเจกต์ สมาชิกที่มี: %s (ตั้งชื่อเล่นได้ที่หน้าตั้งค่า > ทีมและชื่อเล่น)`, who, strings.Join(names, ", ")))
}

// ResolveWorkspaceMember finds a person anywhere in the workspace (for adding them to a project).
func (a *App) ResolveWorkspaceMember(ctx context.Context, who string) (domain.Member, error) {
	members, err := a.tasks().WorkspaceMembers(ctx)
	if err != nil {
		return domain.Member{}, err
	}
	if m := a.matchMember(members, who); m != nil {
		return *m, nil
	}
	return domain.Member{}, domain.UserError(fmt.Sprintf(`ไม่พบ "%s" ใน workspace ของระบบจัดการงาน ต้องเชิญเข้า workspace ก่อน`, who))
}

// matchMember matches a nickname, email or name: exact first, then partial.
func (a *App) matchMember(members []domain.Member, who string) *domain.Member {
	target := who
	if v, ok := a.Store.Nicknames()[who]; ok {
		target = v
	}
	q := domain.Norm(target)
	if q == "" {
		return nil
	}
	for i, m := range members {
		if m.Email != "" && domain.Norm(m.Email) == q {
			return &members[i]
		}
	}
	for i, m := range members {
		for _, n := range []string{m.DisplayName, m.FirstName, m.FirstName + m.LastName} {
			if n != "" && domain.Norm(n) == q {
				return &members[i]
			}
		}
	}
	for i, m := range members {
		for _, n := range []string{m.DisplayName, m.FirstName, m.Email} {
			if n != "" && strings.Contains(domain.Norm(n), q) {
				return &members[i]
			}
		}
	}
	return nil
}

// ResolveEmail gives an email for a person: an address as-is, a nickname mapped to an email, or a member's email.
func (a *App) ResolveEmail(ctx context.Context, projectID, who string) (string, error) {
	w := strings.TrimSpace(who)
	if domain.IsEmail(w) {
		return w, nil
	}
	if mapped := a.Store.Nicknames()[w]; strings.Contains(mapped, "@") {
		return mapped, nil
	}
	if projectID != "" {
		if m, err := a.ResolveMember(ctx, projectID, w); err == nil && m != nil && m.Email != "" {
			return m.Email, nil
		}
	}
	return "", domain.UserError(fmt.Sprintf(`ไม่รู้อีเมลของ "%s" ใส่เป็นอีเมลตรง ๆ หรือจับคู่ชื่อเล่นกับอีเมลในหน้าตั้งค่า > ทีมและชื่อเล่น`, who))
}

func (a *App) ResolveState(ctx context.Context, projectID, name string) (domain.State, error) {
	states, err := a.tasks().States(ctx, projectID)
	if err != nil {
		return domain.State{}, err
	}
	q := domain.Norm(name)
	for _, s := range states {
		if domain.Norm(s.Name) == q {
			return s, nil
		}
	}
	for _, s := range states {
		if strings.Contains(domain.Norm(s.Name), q) {
			return s, nil
		}
	}
	for _, s := range states {
		if string(s.Group) == q || (q == "done" && s.Group == domain.GroupCompleted) || (q == "todo" && s.Group == domain.GroupUnstarted) {
			return s, nil
		}
	}
	names := make([]string, len(states))
	for i, s := range states {
		names[i] = s.Name
	}
	return domain.State{}, domain.UserError(fmt.Sprintf(`ไม่พบสถานะ "%s" สถานะที่มี: %s`, name, strings.Join(names, ", ")))
}

// ResolveModule finds a module by exact, then partial name.
func (a *App) ResolveModule(ctx context.Context, projectID, name string) (domain.Module, error) {
	mods, err := a.tasks().Modules(ctx, projectID)
	if err != nil {
		return domain.Module{}, err
	}
	q := domain.Norm(name)
	for _, m := range mods {
		if domain.Norm(m.Name) == q {
			return m, nil
		}
	}
	for _, m := range mods {
		if n := domain.Norm(m.Name); strings.Contains(n, q) || (n != "" && strings.Contains(q, n)) {
			return m, nil
		}
	}
	if len(mods) == 0 {
		return domain.Module{}, domain.UserError("โปรเจกต์นี้ยังไม่มี module")
	}
	names := make([]string, len(mods))
	for i, m := range mods {
		names[i] = m.Name
	}
	return domain.Module{}, domain.UserError(fmt.Sprintf(`ไม่พบ module "%s" module ที่มี: %s`, name, strings.Join(names, ", ")))
}

func isNeedProject(err error) bool {
	var u *domain.UserFacingError
	return errors.As(err, &u) && strings.HasPrefix(u.Msg, needProjectPrefix)
}
