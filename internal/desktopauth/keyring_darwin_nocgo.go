//go:build darwin && !cgo

package desktopauth

func (systemKeyring) Set(service, account, value string) error {
	return ErrUnavailable
}

func (systemKeyring) Get(service, account string) (string, error) {
	return "", ErrUnavailable
}

func (systemKeyring) Delete(service, account string) error {
	return ErrUnavailable
}
