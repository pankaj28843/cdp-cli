//go:build !unix

package cli

import "github.com/pankaj28843/cdp-cli/internal/processgroup"

func ownedProcessTerminationMode() string {
	return processgroup.TerminationMode()
}
