//go:build darwin && cgo

package desktopauth

// #cgo LDFLAGS: -framework Security -framework CoreFoundation
// #include "keyring_darwin.h"
import "C"

import (
	"strings"
	"unicode/utf8"
	"unsafe"
)

func (systemKeyring) Set(service, account, value string) error {
	if !validKeychainAddress(service, account) || len(value) == 0 || len(value) > maxRecordBytes {
		return ErrUnavailable
	}
	serviceString, accountString := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(serviceString))
	defer C.free(unsafe.Pointer(accountString))
	data := []byte(value)
	defer clear(data)
	return nativeKeychainError(int32(C.routenest_keychain_set(serviceString, accountString, unsafe.Pointer(&data[0]), C.size_t(len(data)))))
}

func (systemKeyring) Get(service, account string) (string, error) {
	if !validKeychainAddress(service, account) {
		return "", ErrUnavailable
	}
	serviceString, accountString := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(serviceString))
	defer C.free(unsafe.Pointer(accountString))
	data := make([]byte, maxRecordBytes)
	defer clear(data)
	var length C.size_t
	status := C.routenest_keychain_get(serviceString, accountString, unsafe.Pointer(&data[0]), C.size_t(len(data)), &length)
	if err := nativeKeychainError(int32(status)); err != nil {
		return "", err
	}
	if length > C.size_t(len(data)) {
		return "", ErrCorrupt
	}
	return string(data[:int(length)]), nil
}

func (systemKeyring) Delete(service, account string) error {
	if !validKeychainAddress(service, account) {
		return ErrUnavailable
	}
	serviceString, accountString := C.CString(service), C.CString(account)
	defer C.free(unsafe.Pointer(serviceString))
	defer C.free(unsafe.Pointer(accountString))
	return nativeKeychainError(int32(C.routenest_keychain_delete(serviceString, accountString)))
}

func validKeychainAddress(service, account string) bool {
	return service != "" && account != "" && utf8.ValidString(service) && utf8.ValidString(account) &&
		!strings.ContainsRune(service, '\x00') && !strings.ContainsRune(account, '\x00')
}
