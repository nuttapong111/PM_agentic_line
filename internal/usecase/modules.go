package usecase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
)

// findModule matches a module by name: exact first, then a name that contains the text.
func findModule(mods []domain.Module, name string) (*domain.Module, error) {
	n := domain.Norm(name)
	var partial []*domain.Module
	for i := range mods {
		mn := domain.Norm(mods[i].Name)
		if mn == n {
			return &mods[i], nil
		}
		if n != "" && strings.Contains(mn, n) {
			partial = append(partial, &mods[i])
		}
	}
	if len(partial) == 1 {
		return partial[0], nil
	}
	names := make([]string, len(mods))
	for i, m := range mods {
		names[i] = m.Name
	}
	if len(partial) > 1 {
		return nil, domain.UserError(fmt.Sprintf(`"%s" ตรงกับหลาย module ระบุให้ชัดขึ้น: %s`, name, strings.Join(names, ", ")))
	}
	return nil, domain.UserError(fmt.Sprintf(`ไม่พบ module "%s" ที่มี: %s`, name, strings.Join(names, ", ")))
}

func dateText(d *string) string {
	if d == nil || *d == "" {
		return "ยังไม่ตั้ง"
	}
	return domain.ThaiDate((*d)[:min(10, len(*d))], true)
}

func (a *App) prepareModuleDates(ctx context.Context, raw json.RawMessage) (domain.Preview, error) {
	var in struct {
		Project string
		Modules []struct {
			Module     string
			StartDate  *string `json:"start_date"`
			TargetDate *string `json:"target_date"`
		}
	}
	if err := decode(raw, &in); err != nil {
		return domain.Preview{}, err
	}
	if len(in.Modules) == 0 {
		return domain.Preview{}, domain.UserError("ระบุ module อย่างน้อย 1 ตัว")
	}
	p, err := a.ResolveProject(ctx, in.Project)
	if err != nil {
		return domain.Preview{}, err
	}
	mods, err := a.tasks().Modules(ctx, p.ID)
	if err != nil {
		return domain.Preview{}, err
	}
	if len(mods) == 0 {
		return domain.Preview{}, domain.UserError(p.Name + " ยังไม่มี module")
	}
	changes := []domain.PlanModule{}
	lines := []domain.PreviewLine{}
	seen := map[string]bool{}
	for _, x := range in.Modules {
		for _, d := range []**string{&x.StartDate, &x.TargetDate} {
			if *d != nil && strings.TrimSpace(**d) == "" {
				*d = nil
			}
		}
		if err := domain.CheckDate(x.StartDate, "start_date"); err != nil {
			return domain.Preview{}, err
		}
		if err := domain.CheckDate(x.TargetDate, "target_date"); err != nil {
			return domain.Preview{}, err
		}
		if x.StartDate == nil && x.TargetDate == nil {
			return domain.Preview{}, domain.UserError("ระบุวันเริ่มหรือวันจบของ " + x.Module)
		}
		m, err := findModule(mods, x.Module)
		if err != nil {
			return domain.Preview{}, err
		}
		if seen[m.ID] {
			return domain.Preview{}, domain.UserError(m.Name + " ซ้ำกัน")
		}
		seen[m.ID] = true
		start, end := m.StartDate, m.TargetDate
		if x.StartDate != nil {
			start = x.StartDate
		}
		if x.TargetDate != nil {
			end = x.TargetDate
		}
		if start != nil && end != nil && *start != "" && *end != "" && (*end)[:min(10, len(*end))] < (*start)[:min(10, len(*start))] {
			return domain.Preview{}, domain.UserError(fmt.Sprintf("วันจบของ %s (%s) ต้องไม่ก่อนวันเริ่ม (%s)", m.Name, dateText(end), dateText(start)))
		}
		changes = append(changes, domain.PlanModule{ModuleID: m.ID, Name: m.Name, ModuleWrite: domain.ModuleWrite{StartDate: x.StartDate, TargetDate: x.TargetDate}})
		lines = append(lines, domain.PreviewLine{
			Text: m.Name,
			Sub:  fmt.Sprintf("%s – %s (เดิม %s – %s)", dateText(start), dateText(end), dateText(m.StartDate), dateText(m.TargetDate)),
		})
	}
	title := "ตั้งวันของ module " + changes[0].Name
	if len(changes) > 1 {
		title = fmt.Sprintf("ตั้งวันของ %d module", len(changes))
	}
	return domain.Preview{
		Title: title, Subtitle: p.Name, Lines: lines, Warnings: []string{}, ConfirmLabel: "ยืนยัน ตั้งวัน",
		Plan: domain.WritePlan{Kind: domain.PlanModules, ProjectID: &p.ID, Items: domain.MustJSON(changes)},
	}, nil
}
