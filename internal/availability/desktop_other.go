//go:build !darwin

package availability

func readDesktopState() desktopState {
	return desktopState{known: true, onConsole: true, unlocked: true, lidOpen: true}
}
