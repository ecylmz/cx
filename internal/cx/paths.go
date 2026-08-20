package cx

import (
	"fmt"
	"os"
	"path/filepath"
)

type paths struct {
	Home, CodexHome, ConfigRoot, DataRoot, CacheRoot, AccountsRoot string
}

func resolvePaths() (paths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return paths{}, err
	}
	xdgConfig := os.Getenv("XDG_CONFIG_HOME")
	if xdgConfig == "" {
		xdgConfig = filepath.Join(home, ".config")
	}
	xdgData := os.Getenv("XDG_DATA_HOME")
	if xdgData == "" {
		xdgData = filepath.Join(home, ".local", "share")
	}
	xdgCache := os.Getenv("XDG_CACHE_HOME")
	if xdgCache == "" {
		xdgCache = filepath.Join(home, ".cache")
	}
	codexHome := os.Getenv("CODEX_HOME")
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	codexHome, err = filepath.Abs(codexHome)
	if err != nil {
		return paths{}, err
	}
	p := paths{
		Home: home, CodexHome: codexHome,
		ConfigRoot: filepath.Join(xdgConfig, "cx"),
		DataRoot:   filepath.Join(xdgData, "cx"),
		CacheRoot:  filepath.Join(xdgCache, "cx"),
	}
	p.AccountsRoot = filepath.Join(p.DataRoot, "accounts")
	return p, nil
}

func (p paths) ensure() error {
	for _, d := range []string{p.ConfigRoot, p.DataRoot, p.CacheRoot, p.AccountsRoot, p.CodexHome} {
		if err := os.MkdirAll(d, 0700); err != nil {
			return fmt.Errorf("create %s: %w", d, err)
		}
		_ = os.Chmod(d, 0700)
	}
	return nil
}

func (p paths) statePath() string            { return filepath.Join(p.DataRoot, "state.json") }
func (p paths) cachePath() string            { return filepath.Join(p.CacheRoot, "status.json") }
func (p paths) sharedAuthPath() string       { return filepath.Join(p.CodexHome, "auth.json") }
func (p paths) sharedConfigPath() string     { return filepath.Join(p.CodexHome, "config.toml") }
func (p paths) accountDir(id string) string  { return filepath.Join(p.AccountsRoot, id) }
func (p paths) accountAuth(id string) string { return filepath.Join(p.accountDir(id), "auth.json") }
func (p paths) accountMeta(id string) string { return filepath.Join(p.accountDir(id), "account.json") }
