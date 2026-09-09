//go:build !unix

package wrapper

import "fmt"

func Exec(name string, args []string) error {
	return fmt.Errorf("%s compatibility exec is currently supported only on Unix", name)
}
