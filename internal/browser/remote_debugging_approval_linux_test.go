//go:build linux

package browser

import (
	"reflect"
	"testing"
)

func TestChromeApplicationNamesIncludesChromiumForStable(t *testing.T) {
	names, ok := chromeApplicationNames("stable")
	if !ok {
		t.Fatal("stable channel is unsupported")
	}
	want := []string{"Google Chrome", "Chromium"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("chromeApplicationNames(stable) = %v, want %v", names, want)
	}
}

func TestChromeApplicationNamesRejectsUnknownChannel(t *testing.T) {
	if names, ok := chromeApplicationNames("nightly"); ok || names != nil {
		t.Fatalf("chromeApplicationNames(nightly) = %v, %t; want nil, false", names, ok)
	}
}
