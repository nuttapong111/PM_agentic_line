package usecase

import (
	"context"
	"time"

	"github.com/nuttapong111/pm_agentic_line/internal/domain"
	"github.com/nuttapong111/pm_agentic_line/internal/port"
)

// NoRecords stands in for the record store when there is no database.
type NoRecords struct{}

func (NoRecords) Enabled() bool { return false }
func (NoRecords) Add(context.Context, string, domain.RecordKind, *string, any, string, *time.Time) (domain.Record, error) {
	return domain.Record{}, domain.ErrNoDatabase
}
func (NoRecords) List(context.Context, string, []domain.RecordKind, domain.ListFilter) ([]domain.Record, error) {
	return []domain.Record{}, nil
}
func (NoRecords) Get(context.Context, string, int64) (*domain.Record, error)        { return nil, nil }
func (NoRecords) GetByCode(context.Context, string, string) (*domain.Record, error) { return nil, nil }
func (NoRecords) Update(context.Context, string, int64, domain.RecordPatch) (*domain.Record, error) {
	return nil, domain.ErrNoDatabase
}
func (NoRecords) DueReminders(context.Context) ([]domain.Record, error) { return nil, nil }

// NoHistory stands in for the history store when there is no database.
type NoHistory struct{}

func (NoHistory) Enabled() bool                                                     { return false }
func (NoHistory) LogActivity(context.Context, string, string, string, *string, any) {}
func (NoHistory) LogUsage(context.Context, string, int64, int64, int64)             {}
func (NoHistory) Snapshot(context.Context, string, domain.ProjectMetrics) error     { return nil }
func (NoHistory) History(context.Context, string, int) (port.HistoryResult, error) {
	return port.HistoryResult{Enabled: false, Points: []port.HistoryPoint{}, Activity: []port.ActivityRow{}, Usage: []port.UsageRow{}}, nil
}
func (NoHistory) MetricsAround(context.Context, string, int) (*port.HistoryPoint, error) {
	return nil, nil
}
func (NoHistory) DataSummary(context.Context) ([]port.WorkspaceSummary, error) {
	return []port.WorkspaceSummary{}, nil
}
func (NoHistory) Export(context.Context, string) (port.ExportData, error) {
	return port.ExportData{}, nil
}
func (NoHistory) DeleteWorkspace(context.Context, *string) error  { return nil }
func (NoHistory) UsageThisMonth(context.Context) (*int, error)    { return nil, nil }
func (NoHistory) BackfillWorkspace(context.Context, string) error { return nil }
