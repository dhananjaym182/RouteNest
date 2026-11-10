#ifndef ROUTENEST_DESKTOPAUTH_KEYRING_DARWIN_H
#define ROUTENEST_DESKTOPAUTH_KEYRING_DARWIN_H

#include <stddef.h>
#include <stdint.h>
#include <stdlib.h>

int32_t routenest_keychain_get(const char *service, const char *account, void *output, size_t capacity, size_t *length);
int32_t routenest_keychain_set(const char *service, const char *account, const void *value, size_t length);
int32_t routenest_keychain_delete(const char *service, const char *account);

#endif
