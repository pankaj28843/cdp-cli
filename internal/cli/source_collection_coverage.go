package cli

// sourceCollectionCoverage makes collection completeness machine-readable
// without turning source-specific browser behavior into a generic scraper API.
type sourceCollectionCoverage struct {
	ObservedRecordKinds        []string `json:"observed_record_kinds"`
	PossiblyMissingRecordKinds []string `json:"possibly_missing_record_kinds"`
	Continuation               string   `json:"continuation"`
	Cursor                     string   `json:"cursor,omitempty"`
	UnresolvedControls         bool     `json:"unresolved_controls"`
	DecodeRejections           int      `json:"decode_rejections"`
	TerminationEvidence        []string `json:"termination_evidence"`
}

func staticSourceCoverage(observed, possiblyMissing []string, status, reason string) sourceCollectionCoverage {
	if observed == nil {
		observed = []string{}
	}
	if possiblyMissing == nil {
		possiblyMissing = []string{}
	}
	evidence := []string{"fully_rendered_document"}
	if status != "exhausted" {
		evidence = []string{"hard_or_requested_limit"}
	}
	if reason != "" {
		evidence = append(evidence, reason)
	}
	return sourceCollectionCoverage{
		ObservedRecordKinds:        observed,
		PossiblyMissingRecordKinds: possiblyMissing,
		Continuation:               "not_applicable",
		TerminationEvidence:        evidence,
	}
}

func dynamicSourceCoverage(observed, possiblyMissing []string, status, reason, continuation, cursor string, unresolvedControls bool, evidence ...string) sourceCollectionCoverage {
	if observed == nil {
		observed = []string{}
	}
	if possiblyMissing == nil {
		possiblyMissing = []string{}
	}
	if len(evidence) == 0 {
		if status == "exhausted" {
			evidence = []string{"source_exhaustion_proven"}
		} else if reason != "" {
			evidence = []string{reason}
		} else {
			evidence = []string{"incomplete_termination_evidence"}
		}
	}
	return sourceCollectionCoverage{
		ObservedRecordKinds:        observed,
		PossiblyMissingRecordKinds: possiblyMissing,
		Continuation:               continuation,
		Cursor:                     cursor,
		UnresolvedControls:         unresolvedControls,
		TerminationEvidence:        evidence,
	}
}
