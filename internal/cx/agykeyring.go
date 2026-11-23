package cx

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

type agyKeyring struct {
	platform string
}

func (k agyKeyring) read() ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch k.platform {
	case "darwin":
		cmd = exec.CommandContext(ctx, "security", "find-generic-password", "-s", "gemini", "-a", "antigravity", "-w")
	case "linux":
		cmd = exec.CommandContext(ctx, "secret-tool", "lookup", "service", "gemini", "username", "antigravity")
	default:
		return nil, fmt.Errorf("Antigravity account switching is unavailable on %s", k.platform)
	}
	out, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) &&
			(k.platform == "linux" && exit.ExitCode() == 1 && len(exit.Stderr) == 0 ||
				k.platform == "darwin" && exit.ExitCode() == 44) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Antigravity keyring: %w", err)
	}
	out = bytes.TrimSpace(out)
	if k.platform == "darwin" {
		if encoded, ok := bytes.CutPrefix(out, []byte("go-keyring-base64:")); ok {
			return base64.StdEncoding.AppendDecode(nil, encoded)
		}
		if encoded, ok := bytes.CutPrefix(out, []byte("go-keyring-encoded:")); ok {
			return hex.AppendDecode(nil, encoded)
		}
		// security prints binary passwords as hexadecimal.
		if decoded, err := hex.AppendDecode(nil, out); err == nil {
			return decoded, nil
		}
	}
	return out, nil
}

func (k agyKeyring) write(credential []byte) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cmd *exec.Cmd
	switch k.platform {
	case "darwin":
		if len(credential) == 0 {
			cmd = exec.CommandContext(ctx, "security", "delete-generic-password", "-s", "gemini", "-a", "antigravity")
		} else {
			line := "add-generic-password -U -s gemini -a antigravity -X "
			// shortcut: security -i reads 4095 bytes per line, use Keychain Services directly for larger credentials.
			if len(line)+hex.EncodedLen(len(credential))+1 > 4095 {
				return errors.New("Antigravity credential is too large for macOS security")
			}
			cmd = exec.CommandContext(ctx, "security", "-i")
			cmd.Stdin = bytes.NewReader(append(hex.AppendEncode([]byte(line), credential), '\n'))
		}
	case "linux":
		if len(credential) == 0 {
			cmd = exec.CommandContext(ctx, "secret-tool", "clear", "service", "gemini", "username", "antigravity")
		} else {
			cmd = exec.CommandContext(ctx, "secret-tool", "store", "--label=Password for 'antigravity' on 'gemini'", "service", "gemini", "username", "antigravity")
			cmd.Stdin = bytes.NewReader(credential)
		}
	default:
		return fmt.Errorf("Antigravity account switching is unavailable on %s", k.platform)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("update Antigravity keyring: %w", err)
	}
	return nil
}

func switchAgyAccount(p paths, a Account, save func() error) error {
	keyring := agyKeyring{platform: runtime.GOOS}
	current, err := keyring.read()
	if err != nil {
		return err
	}
	if len(current) != 0 {
		if err := saveLiveAgyCredential(p, current); err != nil {
			return err
		}
	}
	target, _, err := readAgyCredential(p, a)
	if err != nil {
		return err
	}
	if bytes.Equal(current, target) {
		return save()
	}
	if err := keyring.write(target); err != nil {
		return err
	}
	actual, err := keyring.read()
	if err == nil && !bytes.Equal(actual, target) {
		err = errors.New("Antigravity keyring did not retain the selected credential")
	}
	if err == nil {
		err = save()
	}
	if err != nil {
		return errors.Join(err, keyring.write(current))
	}
	return nil
}

func saveLiveAgyCredential(p paths, credential []byte) error {
	email := agyCredentialEmail(credential)
	if email == "" {
		return errors.New("the Antigravity keyring contains an unsupported credential")
	}
	accounts, err := listAccountsFor(p, providerAgy)
	if err != nil {
		return err
	}
	var imported Account
	for _, a := range accounts {
		_, savedEmail, err := readAgyCredential(p, a)
		if err == nil && strings.EqualFold(savedEmail, email) {
			return atomicWrite(agyCredentialPath(p, a), credential, 0o600)
		}
		if a.ID == agyDefaultID && errors.Is(err, errNoAgyCredential) {
			imported = a
		}
	}
	if imported.ID != "" {
		return atomicWrite(agyCredentialPath(p, imported), credential, 0o600)
	}
	return errors.New("the Antigravity keyring account is not registered; run cx init --provider agy before switching")
}
