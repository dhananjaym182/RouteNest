package desktopauth

import "github.com/zalando/go-keyring"

func nativeKeychainError(status int32) error {
	switch status {
	case 0:
		return nil
	case -25300:
		return keyring.ErrNotFound
	case -26275:
		return ErrCorrupt
	default:
		return ErrUnavailable
	}
}
