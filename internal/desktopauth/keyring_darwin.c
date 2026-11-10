//go:build darwin && cgo

#include "keyring_darwin.h"
#include <CoreFoundation/CoreFoundation.h>
#include <Security/Security.h>
#include <limits.h>
#include <string.h>

static OSStatus routenest_keychain_query(const char *service, const char *account, SecKeychainRef *keychain, CFMutableDictionaryRef *query) {
    *keychain = NULL;
    *query = NULL;
    if (service == NULL || account == NULL || service[0] == '\0' || account[0] == '\0') {
        return errSecParam;
    }
    OSStatus status = SecKeychainCopyDefault(keychain);
    if (status != errSecSuccess || *keychain == NULL) {
        if (*keychain != NULL) {
            CFRelease(*keychain);
            *keychain = NULL;
        }
        return status == errSecSuccess ? errSecNotAvailable : status;
    }
    CFStringRef serviceString = CFStringCreateWithCString(kCFAllocatorDefault, service, kCFStringEncodingUTF8);
    CFStringRef accountString = CFStringCreateWithCString(kCFAllocatorDefault, account, kCFStringEncodingUTF8);
    *query = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (serviceString == NULL || accountString == NULL || *query == NULL) {
        if (serviceString != NULL) {
            CFRelease(serviceString);
        }
        if (accountString != NULL) {
            CFRelease(accountString);
        }
        if (*query != NULL) {
            CFRelease(*query);
            *query = NULL;
        }
        CFRelease(*keychain);
        *keychain = NULL;
        return errSecAllocate;
    }
    const void *keychains[] = {*keychain};
    CFArrayRef searchList = CFArrayCreate(kCFAllocatorDefault, keychains, 1, &kCFTypeArrayCallBacks);
    if (searchList == NULL) {
        CFRelease(serviceString);
        CFRelease(accountString);
        CFRelease(*query);
        CFRelease(*keychain);
        *query = NULL;
        *keychain = NULL;
        return errSecAllocate;
    }
    CFDictionarySetValue(*query, kSecClass, kSecClassGenericPassword);
    CFDictionarySetValue(*query, kSecAttrService, serviceString);
    CFDictionarySetValue(*query, kSecAttrAccount, accountString);
    CFDictionarySetValue(*query, kSecMatchSearchList, searchList);
    CFRelease(searchList);
    CFRelease(accountString);
    CFRelease(serviceString);
    return errSecSuccess;
}

int32_t routenest_keychain_get(const char *service, const char *account, void *output, size_t capacity, size_t *length) {
    if (output == NULL || length == NULL || capacity > LONG_MAX) {
        return errSecParam;
    }
    *length = 0;
    SecKeychainRef keychain = NULL;
    CFMutableDictionaryRef query = NULL;
    OSStatus status = routenest_keychain_query(service, account, &keychain, &query);
    if (status != errSecSuccess) {
        return status;
    }
    CFDictionarySetValue(query, kSecReturnData, kCFBooleanTrue);
    CFDictionarySetValue(query, kSecMatchLimit, kSecMatchLimitOne);
    CFTypeRef result = NULL;
    status = SecItemCopyMatching(query, &result);
    if (status == errSecSuccess) {
        if (result == NULL || CFGetTypeID(result) != CFDataGetTypeID()) {
            status = errSecDecode;
        } else {
            CFIndex size = CFDataGetLength((CFDataRef)result);
            if (size < 0 || (size_t)size > capacity) {
                status = errSecDecode;
            } else {
                if (size != 0) {
                    memcpy(output, CFDataGetBytePtr((CFDataRef)result), (size_t)size);
                }
                *length = (size_t)size;
            }
        }
    }
    if (result != NULL) {
        CFRelease(result);
    }
    CFRelease(query);
    CFRelease(keychain);
    return status;
}

int32_t routenest_keychain_set(const char *service, const char *account, const void *value, size_t length) {
    if (value == NULL || length == 0 || length > LONG_MAX) {
        return errSecParam;
    }
    SecKeychainRef keychain = NULL;
    CFMutableDictionaryRef query = NULL;
    OSStatus status = routenest_keychain_query(service, account, &keychain, &query);
    if (status != errSecSuccess) {
        return status;
    }
    CFDataRef data = CFDataCreate(kCFAllocatorDefault, value, (CFIndex)length);
    CFMutableDictionaryRef changes = CFDictionaryCreateMutable(kCFAllocatorDefault, 0, &kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
    if (data == NULL || changes == NULL) {
        if (data != NULL) {
            CFRelease(data);
        }
        if (changes != NULL) {
            CFRelease(changes);
        }
        CFRelease(query);
        CFRelease(keychain);
        return errSecAllocate;
    }
    CFDictionarySetValue(changes, kSecValueData, data);
    status = SecItemUpdate(query, changes);
    if (status == errSecItemNotFound) {
        CFDictionaryRemoveValue(query, kSecMatchSearchList);
        CFDictionarySetValue(query, kSecUseKeychain, keychain);
        CFDictionarySetValue(query, kSecValueData, data);
        CFDictionarySetValue(query, kSecAttrLabel, CFSTR("RouteNest trusted desktop device"));
        status = SecItemAdd(query, NULL);
    }
    CFRelease(changes);
    CFRelease(data);
    CFRelease(query);
    CFRelease(keychain);
    return status;
}

int32_t routenest_keychain_delete(const char *service, const char *account) {
    SecKeychainRef keychain = NULL;
    CFMutableDictionaryRef query = NULL;
    OSStatus status = routenest_keychain_query(service, account, &keychain, &query);
    if (status != errSecSuccess) {
        return status;
    }
    status = SecItemDelete(query);
    CFRelease(query);
    CFRelease(keychain);
    return status;
}
