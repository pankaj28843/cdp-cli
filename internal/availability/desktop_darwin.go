//go:build darwin && cgo

package availability

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation -framework IOKit
#include <CoreGraphics/CoreGraphics.h>
#include <IOKit/IOKitLib.h>
#include <IOKit/pwr_mgt/IOPM.h>

// Return only boolean state; never send the session dictionary into Go.
static int cdpDesktopState(void) {
    CFDictionaryRef session = CGSessionCopyCurrentDictionary();
    if (!session) return 0;
    CFTypeRef console = CFDictionaryGetValue(session, kCGSessionOnConsoleKey);
    CFTypeRef login = CFDictionaryGetValue(session, kCGSessionLoginDoneKey);
    // WindowServer omits this key for an unlocked session.
    CFTypeRef locked = CFDictionaryGetValue(session, CFSTR("CGSSessionScreenIsLocked"));
    if (!console || CFGetTypeID(console) != CFBooleanGetTypeID() ||
        !login || CFGetTypeID(login) != CFBooleanGetTypeID() ||
        (locked && CFGetTypeID(locked) != CFBooleanGetTypeID())) {
        CFRelease(session);
        return 0;
    }
    int state = 1;
    if (CFBooleanGetValue(console) && CFBooleanGetValue(login)) state |= 2;
    if (!locked || !CFBooleanGetValue(locked)) state |= 4;
    CFRelease(session);

    io_registry_entry_t root = IORegistryEntryFromPath(kIOMainPortDefault, "IOPower:/IOPowerConnection/IOPMrootDomain");
    if (!root) return 0;
    CFTypeRef lid = IORegistryEntryCreateCFProperty(root, CFSTR(kAppleClamshellStateKey), kCFAllocatorDefault, 0);
    IOObjectRelease(root);
    // Desktop Macs have no clamshell property.
    if (!lid) return state | 8;
    if (CFGetTypeID(lid) != CFBooleanGetTypeID()) {
        CFRelease(lid);
        return 0;
    }
    if (!CFBooleanGetValue(lid)) state |= 8;
    CFRelease(lid);
    return state;
}
*/
import "C"

func readDesktopState() desktopState {
	state := int(C.cdpDesktopState())
	return desktopState{known: state&1 != 0, onConsole: state&2 != 0, unlocked: state&4 != 0, lidOpen: state&8 != 0}
}
