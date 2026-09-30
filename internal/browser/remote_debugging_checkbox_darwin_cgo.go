//go:build darwin && cgo

package browser

/*
#cgo LDFLAGS: -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
#include <CoreFoundation/CoreFoundation.h>
#include <unistd.h>

static void cdp_set_ax_timeout(AXUIElementRef element) {
	// AX calls can otherwise wait indefinitely when Chrome is busy or its
	// accessibility server has a stale element. Keep this boundary shorter
	// than the Go-level repair deadline.
	AXUIElementSetMessagingTimeout(element, 0.25);
}

static void cdp_scan_remote_debugging_element(AXUIElementRef element, int inApprovalSheet, int press, int *prompts, int *approved) {
	cdp_set_ax_timeout(element);
	CFTypeRef role = NULL;
	CFTypeRef title = NULL;
	CFTypeRef description = NULL;
	int isApprovalSheet = inApprovalSheet;
	if (AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &role) == kAXErrorSuccess &&
		AXUIElementCopyAttributeValue(element, kAXTitleAttribute, &title) == kAXErrorSuccess) {
		if (CFEqual(role, kAXSheetRole) && CFEqual(title, CFSTR("Allow remote debugging?"))) {
			isApprovalSheet = 1;
			(*prompts)++;
		}
		if (isApprovalSheet && CFEqual(role, kAXButtonRole) &&
			AXUIElementCopyAttributeValue(element, kAXDescriptionAttribute, &description) == kAXErrorSuccess &&
			CFEqual(description, CFSTR("Allow")) && press && *approved == 0) {
			if (AXUIElementPerformAction(element, kAXPressAction) == kAXErrorSuccess) {
				(*approved)++;
			}
		}
	}
	if (role != NULL) CFRelease(role);
	if (title != NULL) CFRelease(title);
	if (description != NULL) CFRelease(description);

	CFTypeRef children = NULL;
	if (AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, &children) != kAXErrorSuccess ||
		CFGetTypeID(children) != CFArrayGetTypeID()) {
		if (children != NULL) CFRelease(children);
		return;
	}
	CFIndex count = CFArrayGetCount((CFArrayRef) children);
	for (CFIndex index = 0; index < count; index++) {
		AXUIElementRef child = (AXUIElementRef) CFArrayGetValueAtIndex((CFArrayRef) children, index);
		cdp_scan_remote_debugging_element(child, isApprovalSheet, press, prompts, approved);
	}
	CFRelease(children);
}

static int cdp_scan_remote_debugging_queue(int pid, int press, int *windows, int *prompts, int *approved) {
	*windows = 0;
	*prompts = 0;
	*approved = 0;
	AXUIElementRef application = AXUIElementCreateApplication((pid_t) pid);
	if (application == NULL) return 0;
	cdp_set_ax_timeout(application);
	CFTypeRef windowList = NULL;
	if (AXUIElementCopyAttributeValue(application, kAXWindowsAttribute, &windowList) == kAXErrorSuccess &&
		CFGetTypeID(windowList) == CFArrayGetTypeID()) {
		*windows = (int) CFArrayGetCount((CFArrayRef) windowList);
	}
	if (*windows > 0 && press) {
		AXUIElementSetAttributeValue(application, kAXFrontmostAttribute, kCFBooleanTrue);
		usleep(300000);
	}
	if (*windows > 0) {
		CFIndex count = CFArrayGetCount((CFArrayRef) windowList);
		for (CFIndex index = 0; index < count; index++) {
			AXUIElementRef window = (AXUIElementRef) CFArrayGetValueAtIndex((CFArrayRef) windowList, index);
			cdp_scan_remote_debugging_element(window, 0, press, prompts, approved);
		}
	}
	if (windowList != NULL) CFRelease(windowList);
	CFRelease(application);
	return 1;
}
*/
import "C"

import (
	"context"
)

func EnableNativeRemoteDebuggingCheckbox(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func ScanNativeRemoteDebuggingApproval(ctx context.Context, processName string, press bool) (NativeRemoteDebuggingApprovalResult, error) {
	pids, err := nativeChromeProcessIDs(ctx, processName)
	if err != nil {
		return NativeRemoteDebuggingApprovalResult{}, err
	}
	result := NativeRemoteDebuggingApprovalResult{}
	for _, pid := range pids {
		var windows, prompts, approved C.int
		C.cdp_scan_remote_debugging_queue(C.int(pid), C.int(boolToInt(press)), &windows, &prompts, &approved)
		result.WindowsScanned += int(windows)
		if press {
			result.ApprovedCount += int(approved)
			result.PromptCountAfter += int(prompts)
		} else {
			result.PromptCountBefore += int(prompts)
		}
	}
	return result, nil
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
