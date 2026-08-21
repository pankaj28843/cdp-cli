package chatgpt

import "testing"

func TestSignedInUIEvidenceReady(t *testing.T) {
	tests := []struct {
		name      string
		signedIn  bool
		signedOut bool
		want      bool
	}{
		{name: "signed-in evidence", signedIn: true, want: true},
		{name: "visible signed-out control rejects evidence", signedIn: true, signedOut: true, want: false},
		{name: "no evidence", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := signedInUIEvidenceReady(
				test.signedIn,
				test.signedOut,
			)
			if got != test.want {
				t.Fatalf("signedInUIEvidenceReady() = %v, want %v", got, test.want)
			}
		})
	}
}
