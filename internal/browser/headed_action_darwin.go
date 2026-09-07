//go:build darwin

package browser

import (
	"context"
	"fmt"
	"strconv"
	"time"
)

// Native repair precedes CDP attachment. Resolve the default-profile browser
// once and address its PID; Launch Services name resolution also sees headless
// Chrome, which must never receive activation or an open-URL event.
func runHeadedChromeAction(ctx context.Context, processName, url string) error {
	channel := ""
	for _, candidate := range []string{"stable", "beta", "dev", "canary"} {
		if name, ok := chromeApplicationName(candidate); ok && name == processName {
			channel = candidate
			break
		}
	}
	if channel == "" {
		return fmt.Errorf("unsupported headed Chrome application")
	}
	pids, err := headedChromeProcessIDs(ctx, processName, channel)
	if err != nil {
		return err
	}
	if len(pids) != 1 {
		return fmt.Errorf("headed Chrome action requires one default-profile browser; found %d", len(pids))
	}
	actionCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()
	_, err = runOwnedBrowserCommand(actionCtx, "osascript", "-l", "JavaScript", "-e", headedChromeActionScript, strconv.Itoa(pids[0]), processName, url)
	return err
}

const headedChromeActionScript = `
ObjC.import('AppKit');
ObjC.import('Foundation');
function run(argv) {
  const pid = Number(argv[0]);
  const app = $.NSRunningApplication.runningApplicationWithProcessIdentifier(pid);
  if (!app || app.isNil() || app.terminated ||
      ObjC.unwrap(app.localizedName) !== argv[1] || Number(app.activationPolicy) !== 0) {
    throw new Error('headed Chrome process is no longer available');
  }
  if (argv[2]) {
    const target = $.NSAppleEventDescriptor.descriptorWithProcessIdentifier(pid);
    const event = $.NSAppleEventDescriptor.appleEventWithEventClassEventIDTargetDescriptorReturnIDTransactionID(
      0x4755524c, 0x4755524c, target, -1, 0);
    event.setParamDescriptorForKeyword($.NSAppleEventDescriptor.descriptorWithString(argv[2]), 0x2d2d2d2d);
    const error = Ref();
    // Wait for delivery, never interact, and never prompt for Automation consent.
    const reply = event.sendEventWithOptionsTimeoutError(0x20013, 5, error);
    if (!reply || reply.isNil()) throw new Error('headed Chrome URL delivery failed');
    const failure = reply.paramDescriptorForKeyword(0x6572726e);
    if (failure && !failure.isNil() && Number(failure.int32Value) !== 0) {
      throw new Error('headed Chrome rejected URL delivery');
    }
  } else if (!app.activateWithOptions(0)) {
    throw new Error('headed Chrome activation was rejected');
  }
  return 'ok';
}
`
