//go:build darwin && cgo

package browser

/*
#cgo LDFLAGS: -framework ApplicationServices -framework CoreGraphics
#include <ApplicationServices/ApplicationServices.h>
#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>
#include <unistd.h>

static void cdp_set_ax_timeout(AXUIElementRef element) {
	// AX calls can otherwise wait indefinitely when Chrome is busy or its
	// accessibility server has a stale element. Keep this boundary shorter
	// than the Go-level repair deadline.
	AXUIElementSetMessagingTimeout(element, 0.25);
}

static int cdp_find_remote_debugging_checkbox(AXUIElementRef application, AXUIElementRef element, CGPoint *point) {
	cdp_set_ax_timeout(element);
	CFTypeRef role = NULL;
	CFTypeRef title = NULL;
	int found = 0;
	AXUIElementCopyAttributeValue(element, kAXRoleAttribute, &role);
	AXUIElementCopyAttributeValue(element, kAXTitleAttribute, &title);
	if (role != NULL && title != NULL &&
		CFEqual(role, kAXCheckBoxRole) &&
		CFEqual(title, CFSTR("Allow remote debugging for this browser instance"))) {
		CFTypeRef position = NULL;
		CFTypeRef size = NULL;
		CGSize checkboxSize = CGSizeZero;
		if (AXUIElementCopyAttributeValue(element, kAXPositionAttribute, &position) == kAXErrorSuccess &&
			AXUIElementCopyAttributeValue(element, kAXSizeAttribute, &size) == kAXErrorSuccess &&
			AXValueGetValue((AXValueRef) position, kAXValueCGPointType, point) &&
			AXValueGetValue((AXValueRef) size, kAXValueCGSizeType, &checkboxSize)) {
			point->x += checkboxSize.width / 2.0;
			point->y += checkboxSize.height / 2.0;
			point->x = (CGFloat) ((int) point->x);
			point->y = (CGFloat) ((int) point->y);
			AXUIElementSetAttributeValue(application, kAXFocusedUIElementAttribute, element);
			AXUIElementSetAttributeValue(element, kAXFocusedAttribute, kCFBooleanTrue);
			found = 1;
		}
		if (position != NULL) CFRelease(position);
		if (size != NULL) CFRelease(size);
	}
	if (role != NULL) CFRelease(role);
	if (title != NULL) CFRelease(title);
	if (found) return 1;

	CFTypeRef children = NULL;
	if (AXUIElementCopyAttributeValue(element, kAXChildrenAttribute, &children) != kAXErrorSuccess ||
		CFGetTypeID(children) != CFArrayGetTypeID()) {
		if (children != NULL) CFRelease(children);
		return 0;
	}
	CFIndex count = CFArrayGetCount((CFArrayRef) children);
	for (CFIndex index = 0; index < count; index++) {
		AXUIElementRef child = (AXUIElementRef) CFArrayGetValueAtIndex((CFArrayRef) children, index);
		int childResult = cdp_find_remote_debugging_checkbox(application, child, point);
		if (childResult > 0) {
			found = childResult;
			break;
		}
	}
	CFRelease(children);
	return found;
}

static int cdp_remote_debugging_checkbox_coordinates(int pid, double *x, double *y) {
	AXUIElementRef application = AXUIElementCreateApplication((pid_t) pid);
	if (application == NULL) return 0;
	cdp_set_ax_timeout(application);
	AXUIElementSetAttributeValue(application, kAXFrontmostAttribute, kCFBooleanTrue);
	usleep(300000);
	CGPoint point = CGPointZero;
	int found = 0;
	CFTypeRef windowList = NULL;
	if (AXUIElementCopyAttributeValue(application, kAXWindowsAttribute, &windowList) == kAXErrorSuccess &&
		CFGetTypeID(windowList) == CFArrayGetTypeID()) {
		CFIndex count = CFArrayGetCount((CFArrayRef) windowList);
		for (CFIndex index = 0; index < count; index++) {
			AXUIElementRef window = (AXUIElementRef) CFArrayGetValueAtIndex((CFArrayRef) windowList, index);
			if (cdp_find_remote_debugging_checkbox(application, window, &point)) {
				found = 1;
				break;
			}
		}
	}
	if (found) {
		*x = point.x;
		*y = point.y;
	}
	if (windowList != NULL) CFRelease(windowList);
	CFRelease(application);
	return found;
}

static int cdp_click_remote_debugging_checkbox(int pid) {
	CGPoint point = CGPointZero;
	double x = 0;
	double y = 0;
	int found = cdp_remote_debugging_checkbox_coordinates(pid, &x, &y);
	point.x = x;
	point.y = y;
	if (found == 1) {
		// The headed Chrome window is frontmost at this point. Post one real
		// session click; a second keyboard action could toggle the checkbox
		// back off on Chrome builds that handle the mouse event immediately.
		CGEventRef down = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, point, kCGMouseButtonLeft);
		CGEventRef up = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, point, kCGMouseButtonLeft);
		if (down != NULL && up != NULL) {
			CGEventPost(kCGHIDEventTap, down);
			usleep(15000);
			CGEventPost(kCGHIDEventTap, up);
		}
		if (down != NULL) CFRelease(down);
		if (up != NULL) CFRelease(up);
	}
	return found;
}

static int cdp_press_remote_debugging_allow(pid_t pid, CGPoint point, CGSize size) {
	// Chrome exposes the exact native button through AX, but AXPress is not
	// honored by every Chrome build. Post a real click at the button center so
	// the native sheet performs its own action without Apple Events.
	point.x += size.width / 2.0;
	point.y += size.height / 2.0;
	CGEventRef down = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseDown, point, kCGMouseButtonLeft);
	CGEventRef up = CGEventCreateMouseEvent(NULL, kCGEventLeftMouseUp, point, kCGMouseButtonLeft);
	if (down == NULL || up == NULL) {
		if (down != NULL) CFRelease(down);
		if (up != NULL) CFRelease(up);
		return 0;
	}
	CGEventPost(kCGSessionEventTap, down);
	usleep(100000);
	CGEventPost(kCGSessionEventTap, up);
	usleep(150000);
	CFRelease(down);
	CFRelease(up);
	return 1;
}

static void cdp_scan_remote_debugging_element(pid_t pid, AXUIElementRef application, AXUIElementRef element, int inApprovalSheet, int press, int *prompts, int *approved) {
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
			CFTypeRef position = NULL;
			CFTypeRef size = NULL;
			CGPoint point = CGPointZero;
			CGSize buttonSize = CGSizeZero;
			if (AXUIElementCopyAttributeValue(element, kAXPositionAttribute, &position) == kAXErrorSuccess &&
				AXUIElementCopyAttributeValue(element, kAXSizeAttribute, &size) == kAXErrorSuccess &&
				AXValueGetValue((AXValueRef) position, kAXValueCGPointType, &point) &&
				AXValueGetValue((AXValueRef) size, kAXValueCGSizeType, &buttonSize) &&
				(AXUIElementSetAttributeValue(application, kAXFocusedUIElementAttribute, element), 1) &&
				(AXUIElementSetAttributeValue(element, kAXFocusedAttribute, kCFBooleanTrue), 1) &&
				(AXUIElementPerformAction(element, kAXPressAction), 1) &&
				cdp_press_remote_debugging_allow((pid_t) pid, point, buttonSize)) {
				(*approved)++;
			}
			if (position != NULL) CFRelease(position);
			if (size != NULL) CFRelease(size);
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
		cdp_scan_remote_debugging_element(pid, application, child, isApprovalSheet, press, prompts, approved);
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
	if (*windows > 0) {
		AXUIElementSetAttributeValue(application, kAXFrontmostAttribute, kCFBooleanTrue);
		usleep(300000);
		CFIndex count = CFArrayGetCount((CFArrayRef) windowList);
		for (CFIndex index = 0; index < count; index++) {
			AXUIElementRef window = (AXUIElementRef) CFArrayGetValueAtIndex((CFArrayRef) windowList, index);
			cdp_scan_remote_debugging_element((pid_t) pid, application, window, 0, press, prompts, approved);
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

func EnableNativeRemoteDebuggingCheckbox(ctx context.Context, processName string) (bool, error) {
	pids, err := nativeChromeProcessIDs(ctx, processName)
	if err != nil {
		return false, err
	}
	for _, pid := range pids {
		if C.cdp_click_remote_debugging_checkbox(C.int(pid)) != 0 {
			return true, nil
		}
	}
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
