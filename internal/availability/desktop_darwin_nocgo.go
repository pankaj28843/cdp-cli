//go:build darwin && !cgo

package availability

// A build without the native adapter must not assume approval is possible.
func readDesktopState() desktopState { return desktopState{} }
