package main

import (
	"os"

	"routenest/internal/accounts"
	"routenest/internal/platform"
	accountstore "routenest/internal/store/accounts"
)

func authorizeEmptyAccountStoreForProcess(requested bool) error {
	if !requested {
		return nil
	}
	return os.Setenv(accounts.InitializeEmptyAccountStoreEnv, "true")
}

func prepareClientAuthenticationStorage(requested bool) error {
	previous, existed := os.LookupEnv(accounts.InitializeEmptyAccountStoreEnv)
	if requested {
		defer func() {
			if existed {
				_ = os.Setenv(accounts.InitializeEmptyAccountStoreEnv, previous)
			} else {
				_ = os.Unsetenv(accounts.InitializeEmptyAccountStoreEnv)
			}
		}()
	}
	if err := authorizeEmptyAccountStoreForProcess(requested); err != nil {
		return err
	}
	return accounts.PoolStorageError()
}

func clientAuthenticationStoragePath() string {
	return platform.ResolveDataPath(accountstore.FileName, nil)
}

func freshClientAuthenticationStorageAllowed() (bool, error) {
	return accounts.FreshPoolInitializationAllowed(clientAuthenticationStoragePath())
}
