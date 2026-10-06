package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// ExpandPath expands a leading `~`, `$VAR` and `${VAR}`, then makes the path
// absolute (relative paths resolve against base) and cleans it.
// An unset environment variable is an error, not an empty string.
func ExpandPath(raw, base string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", errors.New("path is empty")
	}
	p := raw
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = home + p[1:]
	} else if strings.HasPrefix(p, "~") {
		return "", fmt.Errorf("%q: ~user paths are not supported; use an absolute path", raw)
	}

	var missing []string
	p = os.Expand(p, func(name string) string {
		v, ok := os.LookupEnv(name)
		if !ok {
			missing = append(missing, name)
		}
		return v
	})
	if len(missing) > 0 {
		return "", fmt.Errorf("%q: environment variable %s is not set", raw, strings.Join(missing, ", "))
	}

	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	return filepath.Clean(p), nil
}

// RepoPath expands raw and checks that it is a safe, existing directory.
// The returned path is symlink-resolved when the directory exists, so two
// spellings of one repository compare equal. The path is returned even
// alongside an error so callers can report it.
func RepoPath(raw, base string) (string, error) {
	p, err := ExpandPath(raw, base)
	if err != nil {
		return "", err
	}
	if real, err := filepath.EvalSymlinks(p); err == nil {
		p = real
	}
	if err := unsafeRoot(p); err != nil {
		return p, err
	}
	st, err := os.Stat(p)
	if err != nil {
		return p, unwrapPathErr(err)
	}
	if !st.IsDir() {
		return p, errors.New("not a directory")
	}
	return p, nil
}

// unsafeRoot rejects the filesystem root, $HOME and any ancestor of $HOME:
// handing one of those to an agent as a "repository" grants far more access
// than any workspace should.
func unsafeRoot(p string) error {
	if p == string(filepath.Separator) {
		return errors.New("the filesystem root cannot be a repository")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	if real, err := filepath.EvalSymlinks(home); err == nil {
		home = real
	}
	if p == home {
		return errors.New("your home directory cannot be a repository")
	}
	if strings.HasPrefix(home, p+string(filepath.Separator)) {
		return errors.New("a parent of your home directory cannot be a repository")
	}
	return nil
}

func unwrapPathErr(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		if errors.Is(pe.Err, fs.ErrNotExist) {
			return errors.New("does not exist")
		}
		return pe.Err
	}
	return err
}

// Dirs are Polyroot's configuration and cache locations.
type Dirs struct {
	Config string // holds config.yaml and, by convention, contexts/
	Cache  string // holds run/<id>/ generated adapter files
}

// DefaultDirs follows XDG conventions on both macOS and Linux:
//
//	config: $POLYROOT_CONFIG_HOME, else $XDG_CONFIG_HOME/polyroot, else ~/.config/polyroot
//	cache:  $POLYROOT_CACHE_HOME,  else $XDG_CACHE_HOME/polyroot,  else ~/.cache/polyroot
func DefaultDirs() (Dirs, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dirs{}, err
	}
	pick := func(override, xdg, fallback string) string {
		if v := os.Getenv(override); v != "" {
			return v
		}
		if v := os.Getenv(xdg); filepath.IsAbs(v) {
			return filepath.Join(v, "polyroot")
		}
		return filepath.Join(home, fallback, "polyroot")
	}
	return Dirs{
		Config: pick("POLYROOT_CONFIG_HOME", "XDG_CONFIG_HOME", ".config"),
		Cache:  pick("POLYROOT_CACHE_HOME", "XDG_CACHE_HOME", ".cache"),
	}, nil
}

// File is the config file path inside the config directory.
func (d Dirs) File() string { return filepath.Join(d.Config, "config.yaml") }
