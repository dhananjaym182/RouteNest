package adminauth

import (
	"context"
	"encoding/json"
	"testing"
)

func TestEveryNewPasswordEntryPointRejectsBlankPasswords(t *testing.T) {
	for _, action := range []string{"bootstrap", "bootstrap with recovery", "password rotate", "create user", "reset user", "recovery code", "offline recovery", "legacy rotation"} {
		t.Run(action, func(t *testing.T) {
			service, repo := testService(t)
			var admin Credentials
			var user UserView
			var code string
			var err error
			if action == "legacy rotation" {
				repo = newMemoryRepo()
				legacy := LegacyCredential{Salt: "legacy-salt", Hash: hashToken("legacy-saltold password")}
				service, err = NewService(repo, Options{PasswordParams: service.hasher.params, Legacy: &legacy})
				if err != nil {
					t.Fatal(err)
				}
			} else if action != "bootstrap" && action != "bootstrap with recovery" {
				admin = bootstrap(t, service)
			}
			switch action {
			case "reset user":
				user, err = service.CreateLocalUser(t.Context(), admin.Principal, "operator", "operator password", []Role{RoleViewer})
			case "recovery code":
				code, err = service.IssueRecoveryCode(t.Context(), admin.Principal, "permanent admin password", "192.0.2.110")
			case "offline recovery":
				err = RecoverOffline(t.Context(), repo, Options{PasswordParams: service.hasher.params, DeliverBootstrap: func(secret string) error { code = secret; return nil }})
			}
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(repo.state)
			if err != nil {
				t.Fatal(err)
			}
			for _, password := range []string{"", "        ", "\t\r\n\t\r\n  ", "\u00a0\u1680\u2000\u2001\u2002\u2028\u2029\u3000"} {
				service.throttle = NewThrottler()
				var credentials Credentials
				var returnedCode string
				switch action {
				case "bootstrap":
					credentials, err = service.CompleteBootstrap(t.Context(), "one time bootstrap secret", password)
				case "bootstrap with recovery":
					credentials, returnedCode, err = service.CompleteBootstrapWithRecovery(t.Context(), "one time bootstrap secret", password)
				case "password rotate":
					err = service.RotatePassword(t.Context(), admin.Principal, "permanent admin password", password)
				case "create user":
					_, err = service.CreateLocalUser(t.Context(), admin.Principal, "operator", password, []Role{RoleViewer})
				case "reset user":
					err = service.SetLocalPasswordAtRevision(t.Context(), admin.Principal, user.ID, user.Revision, password)
				case "recovery code":
					returnedCode, err = service.RecoverWithCode(t.Context(), code, password, "192.0.2.111")
				case "offline recovery":
					credentials, err = service.CompleteBootstrap(t.Context(), code, password)
				case "legacy rotation":
					credentials, err = service.CompleteLegacyRotation(t.Context(), BreakGlassUsername, "old password", password, "192.0.2.111")
				}
				if err == nil || credentials.Token != "" || returnedCode != "" {
					t.Fatal("blank password was accepted or published authentication proof")
				}
				after, err := json.Marshal(repo.state)
				if err != nil {
					t.Fatal(err)
				}
				if string(before) != string(after) {
					t.Fatal("rejected blank password changed durable authority")
				}
				if err := service.check(); err != nil {
					t.Fatal("invalid password policy locked down the service", err)
				}
			}
		})
	}
}

func TestExistingWhitespacePasswordCanStillAuthenticateAndRehash(t *testing.T) {
	service, repo := testService(t)
	bootstrap(t, service)
	oldHasher, err := NewPasswordHasher(PasswordParams{19 * 1024, 3, 1, 16, 32}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const password = "        "
	phc, err := oldHasher.hash(context.Background(), password)
	if err != nil {
		t.Fatal(err)
	}
	user := repo.state.Users[BreakGlassID]
	user.PasswordPHC = phc
	repo.state.Users[user.ID] = user
	if _, err := service.Login(t.Context(), BreakGlassUsername, password, "192.0.2.112"); err != nil {
		t.Fatal("new-password policy changed an existing password", err)
	}
	updated := repo.state.Users[user.ID].PasswordPHC
	if updated == phc || service.hasher.NeedsRehash(updated) {
		t.Fatal("existing password was not upgraded")
	}
}
