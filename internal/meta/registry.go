package meta

import (
	"fmt"
	"slices"
)

const (
	sessionKeepaliveMinutes  = 60
	discoveryIntervalMinutes = 14 * 24 * 60
)

type MaintenanceAction struct {
	ID                string   `json:"id"`
	Command           []string `json:"command"`
	ValidationCommand []string `json:"validation_command,omitempty"`
	IntervalMinutes   int      `json:"interval_minutes"`
}

type Integration struct {
	ID                 string              `json:"id"`
	DisplayName        string              `json:"display_name"`
	DoctorCommand      []string            `json:"doctor_command"`
	MaintenanceActions []MaintenanceAction `json:"maintenance_actions"`
}

var registry = []Integration{
	{
		ID:            "ask-alex",
		DisplayName:   "ByteByteGo Ask Alex",
		DoctorCommand: []string{"ask-alex", "doctor", "--json"},
		MaintenanceActions: []MaintenanceAction{
			{
				ID:              "session",
				Command:         []string{"ask-alex", "auth", "refresh", "--json"},
				IntervalMinutes: sessionKeepaliveMinutes,
			},
			{
				ID:              "catalog",
				Command:         []string{"ask-alex", "catalog", "refresh", "--json"},
				IntervalMinutes: discoveryIntervalMinutes,
			},
		},
	},
	{
		ID:            "ask-chatgpt",
		DisplayName:   "ChatGPT",
		DoctorCommand: []string{"ask-chatgpt", "doctor", "--json"},
		MaintenanceActions: providerMaintenanceActions(
			"ask-chatgpt",
		),
	},
	{
		ID:            "ask-perplexity",
		DisplayName:   "Perplexity",
		DoctorCommand: []string{"ask-perplexity", "doctor", "--json"},
		MaintenanceActions: providerMaintenanceActions(
			"ask-perplexity",
		),
	},
	{
		ID:            "ask-gemini",
		DisplayName:   "Gemini",
		DoctorCommand: []string{"ask-gemini", "doctor", "--json"},
		MaintenanceActions: providerMaintenanceActions(
			"ask-gemini",
		),
	},
	{
		ID:            "ask-grok",
		DisplayName:   "Grok",
		DoctorCommand: []string{"ask-grok", "doctor", "--json"},
		MaintenanceActions: providerMaintenanceActions(
			"ask-grok",
		),
	},
	{
		ID:            "ask-claude",
		DisplayName:   "Claude",
		DoctorCommand: []string{"ask-claude", "doctor", "--json"},
		MaintenanceActions: providerMaintenanceActions(
			"ask-claude",
		),
	},
	{
		ID:            "ask-tripadvisor",
		DisplayName:   "Tripadvisor",
		DoctorCommand: []string{"ask-tripadvisor", "doctor", "--json"},
		MaintenanceActions: []MaintenanceAction{
			{
				ID:      "session",
				Command: []string{"ask-tripadvisor", "auth", "refresh", "--json"},
				ValidationCommand: []string{
					"ask-tripadvisor",
					"conversations",
					"list",
					"--limit",
					"1",
					"--json",
				},
				IntervalMinutes: sessionKeepaliveMinutes,
			},
		},
	},
}

func providerMaintenanceActions(command string) []MaintenanceAction {
	return []MaintenanceAction{
		{
			ID:      "session",
			Command: []string{command, "auth", "refresh", "--json"},
			ValidationCommand: []string{
				command,
				"conversations",
				"list",
				"--limit",
				"1",
				"--json",
			},
			IntervalMinutes: sessionKeepaliveMinutes,
		},
		{
			ID:              "capabilities",
			Command:         []string{command, "capabilities", "refresh", "--json"},
			IntervalMinutes: discoveryIntervalMinutes,
		},
	}
}

func Integrations() []Integration {
	out := make([]Integration, 0, len(registry))
	for _, integration := range registry {
		copy := integration
		copy.DoctorCommand = slices.Clone(integration.DoctorCommand)
		copy.MaintenanceActions = make(
			[]MaintenanceAction,
			0,
			len(integration.MaintenanceActions),
		)
		for _, action := range integration.MaintenanceActions {
			actionCopy := action
			actionCopy.Command = slices.Clone(action.Command)
			actionCopy.ValidationCommand = slices.Clone(
				action.ValidationCommand,
			)
			copy.MaintenanceActions = append(
				copy.MaintenanceActions,
				actionCopy,
			)
		}
		out = append(out, copy)
	}
	return out
}

func integrationByID(id string) (Integration, error) {
	for _, integration := range registry {
		if integration.ID == id {
			return integration, nil
		}
	}
	known := make([]string, 0, len(registry))
	for _, integration := range registry {
		known = append(known, integration.ID)
	}
	return Integration{}, fmt.Errorf(
		"unknown integration %q; known integrations: %v",
		id,
		known,
	)
}
