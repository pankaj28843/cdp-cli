package cli

func addWorkflowTargetIndex(report map[string]any, targetIndex int) {
	if targetIndex > 0 {
		report["target_index"] = targetIndex
	}
}
