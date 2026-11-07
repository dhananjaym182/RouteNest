//go:build windows

package adminsso

import (
	"fmt"
	"os"
)

func openSecretFile(_ *os.Root, name string) (*os.File, error) {
	return nil, fmt.Errorf("file secret reference %s requires Windows ACL validation; use an environment reference", name)
}

func validateSecretFile(path string, _ os.FileInfo) error {
	return fmt.Errorf("file secret reference %s requires Windows ACL validation; use an environment reference", path)
}
