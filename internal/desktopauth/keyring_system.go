//go:build !darwin

package desktopauth

import "github.com/zalando/go-keyring"

func (systemKeyring) Set(service, account, value string) error {
	return keyring.Set(service, account, value)
}

func (systemKeyring) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (systemKeyring) Delete(service, account string) error {
	return keyring.Delete(service, account)
}
