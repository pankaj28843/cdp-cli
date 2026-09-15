package webagent

import "testing"

func TestPreserveOperationFailureKeepsFirstCause(t *testing.T) {
	r := Result{Stage: StageAttached, Error: &OperationError{Code: "composer_not_ready", ErrClass: "provider"}}
	r.PreserveOperationFailure()
	r.Stage = StageCleanupPending
	r.Error = &OperationError{Code: "cleanup_failed", ErrClass: "cleanup"}
	r.PreserveOperationFailure()
	if got := r.Evidence.OperationFailure; got.Code != "composer_not_ready" || got.Stage != StageAttached {
		t.Fatalf("original cause changed: %+v", got)
	}
}

func TestOperationFailureEvidenceValidation(t *testing.T) {
	e := Evidence{RunID: "run", BuildCommit: "test", BrowserMode: "headed", ReadMode: "rendered",
		OperationFailure: &OperationFailureEvidence{Code: "composer_not_ready", ErrClass: "provider", Stage: StageAttached}}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
	e.OperationFailure.Stage = "invalid"
	if err := e.Validate(); err == nil {
		t.Fatal("invalid stage accepted")
	}
	e.OperationFailure.Stage = StageAttached
	e.OperationFailure.Code = ""
	if err := e.Validate(); err == nil {
		t.Fatal("empty error code accepted")
	}
}
