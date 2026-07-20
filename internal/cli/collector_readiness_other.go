//go:build !unix

package cli

import "os"

func collectorPathOwnedByCurrentUser(os.FileInfo) bool {
	return true
}
