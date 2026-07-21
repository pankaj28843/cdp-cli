package cli

import (
	"strings"
	"testing"
)

func TestLocatorAndActionabilityShareAccessibleNameAlgorithm(t *testing.T) {
	helper := accessibleNameHelpersJS()
	locator := locatorFindExpression("role", "Delete Chat", "menuitem", true, false, "data-testid", 20)
	actionability := actionabilityExpression("button", "click")
	if strings.Count(locator, helper) != 1 || strings.Count(actionability, helper) != 1 {
		t.Fatal("locator and actionability must embed the same accessible-name helper")
	}
	for _, required := range []string{"aria-labelledby", "el.labels", "Array.from(el.labels)"} {
		if !strings.Contains(helper, required) {
			t.Fatalf("shared accessible-name helper missing %q support", required)
		}
	}
}
