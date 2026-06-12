package cli

import (
	"strings"

	"github.com/spf13/cobra"
)

type targetOwnershipMetadata struct {
	RunID        string
	TaskID       string
	RootTaskID   string
	ParentTaskID string
	CreatedBy    string
	Workflow     string
}

type targetOwnershipFilter struct {
	RunID      string
	TaskID     string
	RootTaskID string
}

func addTargetOwnershipFlags(cmd *cobra.Command, metadata *targetOwnershipMetadata, includeCreatedBy bool) {
	cmd.Flags().StringVar(&metadata.RunID, "run-id", "", "record this browser work under a caller-supplied run id")
	cmd.Flags().StringVar(&metadata.TaskID, "task-id", "", "record target ownership under this task id")
	cmd.Flags().StringVar(&metadata.RootTaskID, "root-task-id", "", "record target ownership under this root task id; defaults to --task-id")
	cmd.Flags().StringVar(&metadata.ParentTaskID, "parent-task-id", "", "record this task as a child of the given parent task id")
	if includeCreatedBy {
		cmd.Flags().StringVar(&metadata.CreatedBy, "created-by", "cdp", "record the creator label for cleanup filtering")
	}
}

func addTargetOwnershipFilterFlags(cmd *cobra.Command, filter *targetOwnershipFilter) {
	cmd.Flags().StringVar(&filter.RunID, "run-id", "", "only consider page targets recorded under this run id")
	cmd.Flags().StringVar(&filter.TaskID, "task-id", "", "only consider page targets recorded under this exact task id")
	cmd.Flags().StringVar(&filter.RootTaskID, "root-task-id", "", "only consider page targets recorded under this root task id")
}

func normalizeTargetOwnership(metadata targetOwnershipMetadata, defaultCreatedBy string) (targetOwnershipMetadata, error) {
	metadata.RunID = strings.TrimSpace(metadata.RunID)
	metadata.TaskID = strings.TrimSpace(metadata.TaskID)
	metadata.RootTaskID = strings.TrimSpace(metadata.RootTaskID)
	metadata.ParentTaskID = strings.TrimSpace(metadata.ParentTaskID)
	metadata.CreatedBy = strings.TrimSpace(metadata.CreatedBy)
	metadata.Workflow = strings.TrimSpace(metadata.Workflow)
	if metadata.CreatedBy == "" {
		metadata.CreatedBy = strings.TrimSpace(defaultCreatedBy)
	}

	if metadata.TaskID == "" && metadata.RootTaskID != "" {
		metadata.TaskID = metadata.RootTaskID
	}
	if metadata.TaskID != "" && metadata.RootTaskID == "" {
		metadata.RootTaskID = metadata.TaskID
	}
	if metadata.ParentTaskID != "" && metadata.TaskID == "" {
		return targetOwnershipMetadata{}, commandError(
			"invalid_task_context",
			"usage",
			"--parent-task-id requires --task-id",
			ExitUsage,
			[]string{"cdp open https://example.com --task-id child --root-task-id root --parent-task-id parent --json"},
		)
	}
	if metadata.ParentTaskID != "" && metadata.RootTaskID == metadata.TaskID {
		return targetOwnershipMetadata{}, commandError(
			"invalid_task_context",
			"usage",
			"--parent-task-id requires a root task distinct from --task-id",
			ExitUsage,
			[]string{"cdp open https://example.com --task-id child --root-task-id root --parent-task-id root --json"},
		)
	}
	if metadata.ParentTaskID != "" && metadata.ParentTaskID == metadata.TaskID {
		return targetOwnershipMetadata{}, commandError(
			"invalid_task_context",
			"usage",
			"--parent-task-id must differ from --task-id",
			ExitUsage,
			[]string{"cdp open https://example.com --task-id child --root-task-id root --parent-task-id root --json"},
		)
	}
	return metadata, nil
}

func (metadata targetOwnershipMetadata) hasRunOrTask() bool {
	return strings.TrimSpace(metadata.RunID) != "" ||
		strings.TrimSpace(metadata.TaskID) != "" ||
		strings.TrimSpace(metadata.RootTaskID) != "" ||
		strings.TrimSpace(metadata.ParentTaskID) != ""
}

func (metadata targetOwnershipMetadata) targetTaskIDs(targetID string) map[string]string {
	if strings.TrimSpace(metadata.TaskID) == "" || strings.TrimSpace(targetID) == "" {
		return map[string]string{}
	}
	return map[string]string{targetID: metadata.TaskID}
}

func (metadata targetOwnershipMetadata) summary(targetID string) map[string]any {
	return map[string]any{
		"run_id":          metadata.RunID,
		"task_id":         metadata.TaskID,
		"root_task_id":    metadata.RootTaskID,
		"parent_task_id":  metadata.ParentTaskID,
		"created_by":      metadata.CreatedBy,
		"workflow":        metadata.Workflow,
		"target_task_ids": metadata.targetTaskIDs(targetID),
	}
}

func (metadata targetOwnershipMetadata) addTo(data map[string]any, targetID string) {
	data["run_id"] = metadata.RunID
	data["task_id"] = metadata.TaskID
	data["root_task_id"] = metadata.RootTaskID
	data["parent_task_id"] = metadata.ParentTaskID
	data["created_by"] = metadata.CreatedBy
	data["target_task_ids"] = metadata.targetTaskIDs(targetID)
	data["ownership"] = metadata.summary(targetID)
}

func normalizeTargetOwnershipFilter(filter targetOwnershipFilter) targetOwnershipFilter {
	return targetOwnershipFilter{
		RunID:      strings.TrimSpace(filter.RunID),
		TaskID:     strings.TrimSpace(filter.TaskID),
		RootTaskID: strings.TrimSpace(filter.RootTaskID),
	}
}

func (filter targetOwnershipFilter) isSet() bool {
	return strings.TrimSpace(filter.RunID) != "" ||
		strings.TrimSpace(filter.TaskID) != "" ||
		strings.TrimSpace(filter.RootTaskID) != ""
}

func (filter targetOwnershipFilter) matches(record pageCleanupRecord) bool {
	filter = normalizeTargetOwnershipFilter(filter)
	if filter.RunID != "" && record.RunID != filter.RunID {
		return false
	}
	if filter.TaskID != "" && record.TaskID != filter.TaskID {
		return false
	}
	if filter.RootTaskID != "" && record.RootTaskID != filter.RootTaskID {
		return false
	}
	return true
}

func targetTaskIDsForCandidates(candidates []cleanupCandidate) map[string]string {
	targetTaskIDs := map[string]string{}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Target.TargetID) == "" || strings.TrimSpace(candidate.TaskID) == "" {
			continue
		}
		targetTaskIDs[candidate.Target.TargetID] = candidate.TaskID
	}
	return targetTaskIDs
}
