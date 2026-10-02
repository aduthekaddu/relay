package auth

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aduthekaddu/relay/internal/secret"
	"github.com/aduthekaddu/relay/internal/store"
)

func recoveryFixture(t *testing.T) (*Accounts, *store.Store, *user) {
	t.Helper()
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a, err := OpenAccounts(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetPassword(context.Background(), "fixture", testPassword); err != nil {
		t.Fatal(err)
	}
	// Synthetic credentials stay inside the fixture. Never include them in failures.
	_, err = st.DB.Exec(`UPDATE auth_user SET totp_secret=?,totp_pending=?,totp_last_step=123 WHERE id=1`, "JBSWY3DPEHPK3PXP", "KRSXG5DSNFXGO===")
	if err != nil {
		t.Fatal(err)
	}
	u, err := a.user(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := a.insertSession(context.Background(), &sessionRow{ID: "s_fixture", TokenHash: "synthetic-hash", Username: u.Username, Method: "password", CreatedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour)}, u.RecoveryGeneration); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateToken(context.Background(), "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB.Exec(`INSERT INTO auth_passkeys(id,credential_id,public_key,name,created_at) VALUES('pk_fixture',x'01',x'02','fixture',0)`); err != nil {
		t.Fatal(err)
	}
	return a, st, u
}

func TestRecoverTOTPLifecycle(t *testing.T) {
	a, st, old := recoveryFixture(t)
	ctx := context.Background()
	name, err := a.RecoverTOTP(ctx, "renamed", "new synthetic password")
	if err != nil || name != "renamed" {
		t.Fatal("recovery failed")
	}
	u, err := a.user(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if u.TOTPSecret != "" || u.TOTPPending != "" || u.TOTPLastStep != 0 || u.RecoveryGeneration != old.RecoveryGeneration+1 {
		t.Fatal("TOTP state was not cleared")
	}
	if _, err := secret.VerifyPassword("new synthetic password", u.PasswordHash); err != nil {
		t.Fatal("new password failed")
	}
	if _, err := secret.VerifyPassword(testPassword, u.PasswordHash); err == nil {
		t.Fatal("old password survived")
	}
	for table, want := range map[string]int{"auth_sessions": 0, "auth_tokens": 1, "auth_passkeys": 1, "auth_audit": 1} {
		var n int
		if err := st.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != want {
			t.Fatalf("%s count = %d, want %d", table, n, want)
		}
	}
	entries, err := a.Activity(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(entries)
	for _, value := range []string{old.TOTPSecret, old.TOTPPending, old.PasswordHash, "new synthetic password"} {
		if strings.Contains(string(b), value) {
			t.Fatal("audit leaked a credential")
		}
	}
	if entries[0].Event != "totp.recover" || entries[0].IP != "local" {
		t.Fatal("missing local recovery audit")
	}
	// Stale password/TOTP requests must not resurrect credentials or sessions.
	for label, write := range map[string]func() error{
		"password": func() error { return a.setPasswordHash(ctx, old.PasswordHash, old) },
		"pending":  func() error { return a.setTOTPPending(ctx, old.TOTPPending, old) },
		"enable":   func() error { return a.enableTOTP(ctx, old.TOTPPending, 124, old) },
		"disable":  func() error { return a.disableTOTP(ctx, old) },
		"session": func() error {
			return a.insertSession(ctx, &sessionRow{ID: "s_stale", TokenHash: "stale-hash", Username: "renamed", ExpiresAt: time.Now().Add(time.Hour)}, old.RecoveryGeneration)
		},
	} {
		if err := write(); err == nil {
			t.Fatalf("stale %s write succeeded", label)
		}
	}
	if _, err := a.RecoverTOTP(ctx, "", "another synthetic password"); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverTOTPAtomicErrorsAndRedaction(t *testing.T) {
	for _, table := range []string{"auth_user", "auth_sessions", "auth_audit"} {
		t.Run(table, func(t *testing.T) {
			a, st, old := recoveryFixture(t)
			verb := "UPDATE"
			if table == "auth_sessions" {
				verb = "DELETE"
			}
			if table == "auth_audit" {
				verb = "INSERT"
			}
			_, err := st.DB.Exec(`CREATE TRIGGER fail_recovery BEFORE ` + verb + ` ON ` + table + ` BEGIN SELECT RAISE(ABORT, 'JBSWY3DPEHPK3PXP'); END`)
			if err != nil {
				t.Fatal(err)
			}
			_, err = a.RecoverTOTP(context.Background(), "", "new synthetic password")
			if err == nil {
				t.Fatal("expected recovery failure")
			}
			if strings.Contains(err.Error(), old.TOTPSecret) || strings.Contains(err.Error(), old.PasswordHash) {
				t.Fatal("error leaked a credential")
			}
			u, err := a.user(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if u.PasswordHash != old.PasswordHash || u.TOTPSecret != old.TOTPSecret || u.TOTPPending != old.TOTPPending || u.RecoveryGeneration != old.RecoveryGeneration {
				t.Fatal("failed recovery changed credentials")
			}
			var n int
			st.DB.QueryRow(`SELECT count(*) FROM auth_sessions`).Scan(&n)
			if n != 1 {
				t.Fatal("failed recovery removed sessions")
			}
			st.DB.QueryRow(`SELECT count(*) FROM auth_audit`).Scan(&n)
			if n != 0 {
				t.Fatal("failed recovery recorded success")
			}
		})
	}
}

func TestRecoverTOTPNoAccountOrInvalidPassword(t *testing.T) {
	st, err := store.OpenMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	a, err := OpenAccounts(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverTOTP(context.Background(), "fixture", testPassword); err == nil {
		t.Fatal("recovery created an account")
	}
	if _, err := a.SetPassword(context.Background(), "fixture", testPassword); err != nil {
		t.Fatal(err)
	}
	if _, err := a.RecoverTOTP(context.Background(), "", "short"); err == nil {
		t.Fatal("weak password accepted")
	}
}

func TestRecoveryHTTPDevicesAndRemoteBoundary(t *testing.T) {
	e := newEnv(t, "127.0.0.1")
	b := e.browser(uaMacChrome)
	b.expect(200, "POST", "/api/v1/auth/setup", map[string]string{"username": "fixture", "password": testPassword})
	other := e.browser(uaAndroidFox)
	other.expect(200, "POST", "/api/v1/auth/login", map[string]string{"username": "fixture", "password": testPassword})
	ctx := context.Background()
	u, err := e.svc.acc.user(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.svc.acc.enableTOTP(ctx, "JBSWY3DPEHPK3PXP", 0, &user{RecoveryGeneration: u.RecoveryGeneration}); err == nil {
		t.Fatal("enable accepted missing pending secret")
	}
	if err := e.svc.acc.setTOTPPending(ctx, "JBSWY3DPEHPK3PXP", u); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.acc.enableTOTP(ctx, "JBSWY3DPEHPK3PXP", 0, u); err != nil {
		t.Fatal(err)
	}
	b.expect(400, "POST", "/api/v1/auth/totp/disable", map[string]string{"code": "invalid"})
	e.browser(uaAndroidFox).expect(401, "POST", "/api/v1/auth/totp/disable", map[string]string{"code": "invalid"})
	if _, err := e.svc.acc.RecoverTOTP(ctx, "", "new synthetic password"); err != nil {
		t.Fatal(err)
	}
	b.expect(401, "GET", "/api/v1/test/whoami", nil)
	other.expect(401, "GET", "/api/v1/test/whoami", nil)
	fresh := e.browser(uaMacChrome)
	fresh.expect(401, "POST", "/api/v1/auth/login", map[string]string{"username": "fixture", "password": testPassword})
	fresh.expect(200, "POST", "/api/v1/auth/login", map[string]string{"username": "fixture", "password": "new synthetic password"})
	fresh.expect(200, "GET", "/api/v1/test/whoami", nil)
}
