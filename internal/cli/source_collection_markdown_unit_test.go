package cli

import (
	"strings"
	"testing"

	"github.com/pankaj28843/cdp-cli/internal/sourcecollect/x"
)

func TestSourceCollectionMarkdownExplainsEmptyInvalidCollection(t *testing.T) {
	got := xCollectionMarkdown("https://x.com/example", x.KindProfilePosts, nil,
		map[string]any{"count": 0, "limit": 100, "status": "invalid", "partial_reason": "profile_handle_changed_requires_stable_account_verification"},
		dynamicSourceCoverage(nil, []string{"post"}, "invalid", "profile_handle_changed_requires_stable_account_verification", "profile_identity", "", true))
	for _, want := range []string{"# X collection", "Records: 0 of 100", "Status: `invalid`", "profile_handle_changed_requires_stable_account_verification", "## Records"} {
		if !strings.Contains(got, want) {
			t.Fatalf("Markdown missing %q:\n%s", want, got)
		}
	}
}
