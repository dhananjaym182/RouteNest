//go:build windows

package adminsso

import (
	"fmt"
	"os"
)

func validateSecretFile(path string, _ os.FileInfo) error {
	return fmt.Errorf("file secret reference %s requires Windows ACL validation; use an environment reference", path)
}
