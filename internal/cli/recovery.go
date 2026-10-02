package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/aduthekaddu/relay/internal/auth"
	"github.com/aduthekaddu/relay/internal/config"
	"github.com/aduthekaddu/relay/internal/store"
)

// Recovery opens existing private state only. It never creates an account,
// repairs permissions or contacts a control socket (including RELAY_SOCKET).
func runTOTPRecovery(ctx context.Context, fs *flag.FlagSet) error {
	if flagBool(fs, "keep-sessions") {
		return &ExitError{Code: 2, Msg: "--reset-totp cannot be combined with --keep-sessions; nothing changed"}
	}
	p, err := config.ResolvePaths()
	if err != nil {
		return errors.New("resolve recovery paths failed; nothing changed")
	}
	if err := checkRecoveryState(p); err != nil {
		return err
	}
	var password string
	if flagBool(fs, "stdin") {
		password, err = readPasswordLine(os.Stdin)
	} else {
		password, err = promptNewPassword("the Relay account")
	}
	if err != nil {
		return err
	}
	st, err := store.Open(p.DB)
	if err != nil {
		return errors.New("open recovery database failed; nothing changed")
	}
	defer st.Close()
	acc, err := auth.OpenAccounts(ctx, st)
	if err != nil {
		return errors.New("prepare recovery database failed; nothing changed")
	}
	name, err := acc.RecoverTOTP(ctx, strings.TrimSpace(flagString(fs, "user")), password)
	if err != nil {
		return userError(err)
	}
	fmt.Printf("Password updated for %s. TOTP disabled. All browser sessions signed out. API tokens and passkeys remain valid. Re-enroll TOTP after signing in.\n", name)
	return nil
}

func checkRecoveryState(p config.Paths) error {
	for _, item := range []struct {
		path          string
		dir, optional bool
	}{
		{p.DataDir, true, false}, {p.DB, false, false}, {p.DB + "-wal", false, true}, {p.DB + "-shm", false, true},
	} {
		info, err := os.Lstat(item.path)
		if item.optional && os.IsNotExist(err) {
			continue
		}
		if err != nil || !privateRecoveryFile(info, item.dir, os.Geteuid()) {
			return errors.New("recovery requires an existing database owned by the current OS user, a 0700 data directory and 0600 database files; refuse symlinks; nothing changed")
		}
	}
	return nil
}

func privateRecoveryFile(info os.FileInfo, dir bool, uid int) bool {
	if info == nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(stat.Uid) != uid {
		return false
	}
	mode := info.Mode()
	if dir {
		return mode.IsDir() && mode.Perm() == 0o700
	}
	return mode.IsRegular() && mode.Perm() == 0o600 && stat.Nlink == 1
}
