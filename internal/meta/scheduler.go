package meta

import (
	"time"
)

type IntegrationStatus struct {
	Integration
	Enrollment       Enrollment       `json:"enrollment"`
	LastMaintenance  *ProviderHistory `json:"last_maintenance"`
	MaintenanceState string           `json:"maintenance_state"`
	Due              bool             `json:"due"`
}

type MaintenanceStatusReport struct {
	OK            bool                `json:"ok"`
	StatePath     string              `json:"state_path"`
	EnrolledCount int                 `json:"enrolled_count"`
	EnabledCount  int                 `json:"enabled_count"`
	UpdatedAt     float64             `json:"updated_at"`
	DueCount      int                 `json:"due_count"`
	Integrations  []IntegrationStatus `json:"integrations"`
}

type MaintenancePlanReport struct {
	OK           bool             `json:"ok"`
	TickMinutes  int              `json:"tick_minutes"`
	JobCount     int              `json:"job_count"`
	Jobs         []map[string]any `json:"jobs"`
	Integrations []string         `json:"integrations"`
}

func maintenanceStatus(
	state SchedulerState,
	history LastRunState,
	now time.Time,
) (MaintenanceStatusReport, error) {
	path, err := schedulerPath()
	if err != nil {
		return MaintenanceStatusReport{}, err
	}
	report := MaintenanceStatusReport{
		OK:            true,
		StatePath:     displayPath(path),
		EnrolledCount: len(state.Enrollments),
		UpdatedAt:     state.UpdatedAt,
		Integrations:  make([]IntegrationStatus, 0, len(registry)),
	}
	for _, integration := range Integrations() {
		enrollment := state.Enrollments[integration.ID]
		status := IntegrationStatus{
			Integration:      integration,
			Enrollment:       enrollment,
			MaintenanceState: "disabled",
		}
		if prior, exists := history.LastRuns[integration.ID]; exists {
			copy := prior
			status.LastMaintenance = &copy
		}
		if enrollment.Enabled {
			report.EnabledCount++
			due := dueActions(
				integration.MaintenanceActions,
				enrollment,
				history.LastRuns[integration.ID],
				now,
			)
			status.Due = len(due) > 0
			switch {
			case status.LastMaintenance == nil:
				status.MaintenanceState = "never-run"
			case !status.LastMaintenance.OK:
				status.MaintenanceState = "failed"
			case status.Due:
				status.MaintenanceState = "due"
			default:
				status.MaintenanceState = "fresh"
			}
			if status.Due {
				report.DueCount++
			}
		}
		report.Integrations = append(report.Integrations, status)
	}
	return report, nil
}

func maintenancePlan(state SchedulerState) MaintenancePlanReport {
	report := MaintenancePlanReport{
		OK:           true,
		TickMinutes:  1,
		Jobs:         []map[string]any{},
		Integrations: []string{},
	}
	for _, integration := range registry {
		if state.Enrollments[integration.ID].Enabled {
			report.Integrations = append(
				report.Integrations,
				integration.ID,
			)
		}
	}
	if len(report.Integrations) > 0 {
		report.JobCount = 1
		report.Jobs = append(report.Jobs, map[string]any{
			"kind": "coordinator",
			"cron": "* * * * *",
		})
	}
	return report
}
