package desktopauth

import (
	"crypto/sha256"
	"encoding/hex"
)

const (
	serviceName = "RouteNest"
	accountName = "desktop-trusted-device"
)

type Keyring interface {
	Set(service, account, value string) error
	Get(service, account string) (string, error)
	Delete(service, account string) error
}

var systemExecutor = newKeyringExecutor()

type systemKeyring struct{}

func NewManager() *Manager {
	return NewManagerWithKeyring(systemKeyring{})
}

func NewManagerForInstallation(installation string) *Manager {
	var keyring Keyring = systemKeyring{}
	if installation == "" {
		keyring = nil
	}
	return newManager(keyring, installationSlot(installation), defaultKeyringTimeout, systemExecutor)
}

func installationSlot(installation string) credentialSlot {
	digest := sha256.Sum256([]byte(installation))
	return credentialSlot{serviceName, accountName + ":" + hex.EncodeToString(digest[:])}
}
