package desktopauth

import (
	"context"
	"crypto/subtle"
	"errors"
	"sync"
	"time"

	"github.com/zalando/go-keyring"

	"routenest/internal/adminauth"
)

const defaultKeyringTimeout = 5 * time.Second

var (
	ErrMissing           = errors.New("desktop credential is not stored")
	ErrCorrupt           = errors.New("desktop credential storage is corrupt")
	ErrUnavailable       = errors.New("OS-protected desktop credential storage is unavailable")
	ErrInvalidCredential = errors.New("invalid desktop credential")
	ErrVerification      = errors.New("desktop credential storage verification failed")
)

type credentialSlot struct {
	service string
	account string
}

type Manager struct {
	mu       sync.Mutex
	keyring  Keyring
	executor *keyringExecutor
	slot     credentialSlot
	timeout  time.Duration
	status   adminauth.DesktopCredentialStorageStatus
}

type managerOperation struct {
	manager *Manager
	ctx     context.Context
	mutated bool
	status  adminauth.DesktopCredentialStorageStatus
}

func NewManagerWithKeyring(keyring Keyring) *Manager {
	return NewManagerWithKeyringTimeout(keyring, defaultKeyringTimeout)
}

func NewManagerWithKeyringTimeout(keyring Keyring, timeout time.Duration) *Manager {
	executor := systemExecutor
	if _, system := keyring.(systemKeyring); !system {
		executor = newKeyringExecutor()
	}
	return newManager(keyring, credentialSlot{serviceName, accountName}, timeout, executor)
}

func newManager(keyring Keyring, slot credentialSlot, timeout time.Duration, executor *keyringExecutor) *Manager {
	if timeout <= 0 {
		timeout = defaultKeyringTimeout
	}
	return &Manager{
		keyring:  keyring,
		executor: executor,
		slot:     slot,
		timeout:  timeout,
		status:   storageStatus("unavailable", false),
	}
}

func (*Manager) String() string   { return "[redacted desktop credential manager]" }
func (*Manager) GoString() string { return "[redacted desktop credential manager]" }

func (m *Manager) Inspect() adminauth.DesktopCredentialStorageStatus {
	_, status, _ := m.execute(false, (*managerOperation).read)
	return status
}

func (m *Manager) Credential() (adminauth.TrustedDeviceCredential, adminauth.DesktopCredentialStorageStatus, error) {
	record, status, err := m.execute(false, (*managerOperation).read)
	if err != nil {
		return adminauth.TrustedDeviceCredential{}, status, err
	}
	return record.Credential, status, nil
}

func (m *Manager) Store(credential adminauth.TrustedDeviceCredential) error {
	if !validCredential(credential) {
		return ErrInvalidCredential
	}
	_, _, err := m.execute(false, func(operation *managerOperation) (credentialRecord, error) {
		if _, err := operation.read(); err != nil && !errors.Is(err, ErrMissing) {
			return credentialRecord{}, err
		}
		return credentialRecord{}, operation.write(credentialRecord{
			Version:         recordVersion,
			Credential:      credential,
			AutomaticSignIn: true,
		})
	})
	return err
}

func (m *Manager) Remove() error {
	_, _, err := m.execute(true, func(operation *managerOperation) (credentialRecord, error) {
		operation.markMutation()
		_, err := m.executor.call(operation.ctx, func() (string, error) {
			return "", m.keyring.Delete(m.slot.service, m.slot.account)
		})
		if err != nil && !errors.Is(err, keyring.ErrNotFound) {
			return credentialRecord{}, ErrUnavailable
		}
		_, err = operation.read()
		if errors.Is(err, ErrMissing) {
			return credentialRecord{}, nil
		}
		if err != nil {
			return credentialRecord{}, err
		}
		return credentialRecord{}, ErrVerification
	})
	return err
}

func (m *Manager) SetAutoSignIn(enabled bool) error {
	_, _, err := m.execute(false, func(operation *managerOperation) (credentialRecord, error) {
		record, err := operation.read()
		if err != nil {
			return credentialRecord{}, err
		}
		if record.AutomaticSignIn == enabled {
			return credentialRecord{}, nil
		}
		record.AutomaticSignIn = enabled
		return credentialRecord{}, operation.write(record)
	})
	return err
}

func (m *Manager) Status() adminauth.DesktopCredentialStorageStatus {
	if m.executor != nil && m.executor.needsReconciliation(m.slot) {
		return storageStatus("unavailable", false)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) setStatus(status adminauth.DesktopCredentialStorageStatus) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status = status
}

func (m *Manager) execute(reconcile bool, run func(*managerOperation) (credentialRecord, error)) (credentialRecord, adminauth.DesktopCredentialStorageStatus, error) {
	unavailable := storageStatus("unavailable", false)
	if m.keyring == nil || m.executor == nil {
		m.setStatus(unavailable)
		return credentialRecord{}, unavailable, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(context.Background(), m.timeout)
	defer cancel()
	if !m.executor.acquire(ctx) {
		m.setStatus(unavailable)
		return credentialRecord{}, unavailable, ErrUnavailable
	}
	defer m.executor.release()
	if !reconcile && m.executor.needsReconciliation(m.slot) {
		m.setStatus(unavailable)
		return credentialRecord{}, unavailable, ErrUnavailable
	}
	operation := &managerOperation{manager: m, ctx: ctx, status: unavailable}
	record, err := run(operation)
	if ctx.Err() != nil {
		err = ErrUnavailable
		operation.status = unavailable
	}
	if operation.mutated {
		if err == nil {
			m.executor.reconciled(m.slot)
		} else {
			operation.status = unavailable
		}
	}
	if err != nil {
		record = credentialRecord{}
	}
	m.setStatus(operation.status)
	return record, operation.status, err
}

func (operation *managerOperation) read() (credentialRecord, error) {
	m := operation.manager
	value, err := m.executor.call(operation.ctx, func() (string, error) {
		return m.keyring.Get(m.slot.service, m.slot.account)
	})
	if errors.Is(err, keyring.ErrNotFound) {
		operation.status = storageStatus("missing", false)
		return credentialRecord{}, ErrMissing
	}
	if errors.Is(err, ErrCorrupt) {
		operation.status = storageStatus("corrupt", false)
		return credentialRecord{}, ErrCorrupt
	}
	if err != nil {
		operation.status = storageStatus("unavailable", false)
		return credentialRecord{}, ErrUnavailable
	}
	record, err := decodeRecord(value)
	if err != nil {
		operation.status = storageStatus("corrupt", false)
		return credentialRecord{}, ErrCorrupt
	}
	operation.status = storageStatus("stored", record.AutomaticSignIn)
	return record, nil
}

func (operation *managerOperation) markMutation() {
	operation.mutated = true
	operation.manager.executor.requireReconciliation(operation.manager.slot)
}

func (operation *managerOperation) write(record credentialRecord) error {
	value, err := encodeRecord(record)
	if err != nil {
		return err
	}
	m := operation.manager
	operation.markMutation()
	_, writeErr := m.executor.call(operation.ctx, func() (string, error) {
		return "", m.keyring.Set(m.slot.service, m.slot.account, value)
	})
	if operation.ctx.Err() != nil {
		return ErrUnavailable
	}
	stored, readErr := operation.read()
	if writeErr != nil {
		return ErrUnavailable
	}
	if readErr != nil {
		if errors.Is(readErr, ErrMissing) {
			return ErrVerification
		}
		return readErr
	}
	if stored.Version != record.Version || stored.AutomaticSignIn != record.AutomaticSignIn ||
		stored.Credential.ID != record.Credential.ID ||
		!stored.Credential.ExpiresAt.Equal(record.Credential.ExpiresAt) ||
		subtle.ConstantTimeCompare([]byte(stored.Credential.Secret), []byte(record.Credential.Secret)) != 1 {
		return ErrVerification
	}
	return nil
}

func storageStatus(state string, automaticSignIn bool) adminauth.DesktopCredentialStorageStatus {
	return adminauth.DesktopCredentialStorageStatus{
		Available:       state != "unavailable",
		Stored:          state == "stored",
		AutomaticSignIn: state == "stored" && automaticSignIn,
		State:           state,
	}
}
