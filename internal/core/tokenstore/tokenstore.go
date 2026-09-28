// Package tokenstore keeps a sign-in between commands: a profile's token_cache.
//
//   - memory: nowhere. The sign-in lasts one command and is gone after.
//   - file (the default): a JSON file that only your account may read (mode 0600), as the
//     Azure CLI keeps its own tokens on Linux. Anyone who can act as you, or as root, or
//     who gets a copy of the file can use what is in it.
//
// A refresh token is as good as a sign-in until it expires or is revoked, so a file that
// other accounts can read is refused rather than used, as ssh refuses a readable key.
// Two commands can run at once: the file is changed under a lock file, with a temporary
// file of its own renamed into place, so neither loses the other's sign-in.
package tokenstore

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

// Caches are where a sign-in can be kept between commands: a profile's token_cache.
var Caches = []string{"file", "keychain", "memory"}

// DefaultCache is where a sign-in is kept unless a profile says otherwise.
const DefaultCache = "file"

// FileEnv names another file to keep sign-ins in.
var FileEnv = brand.EnvVar("TOKEN_CACHE")

// Store is named secrets that outlast one command (or, for Memory, do not).
type Store interface {
	// Load is the value kept under key, and whether there is one.
	Load(key string) (string, bool, error)
	// Save keeps value under key, replacing what was there.
	Save(key, value string) error
	// Delete forgets key: true when there was something to forget.
	Delete(key string) (bool, error)
}

// Memory keeps values for this run alone.
type Memory struct {
	mu     sync.Mutex
	values map[string]string
}

// NewMemory is an empty memory store.
func NewMemory() *Memory { return &Memory{values: map[string]string{}} }

// Load is the value kept under key.
func (m *Memory) Load(key string) (string, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.values[key]
	return value, ok, nil
}

// Save keeps value under key.
func (m *Memory) Save(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.values[key] = value
	return nil
}

// Delete forgets key.
func (m *Memory) Delete(key string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.values[key]
	delete(m.values, key)
	return ok, nil
}

// File keeps values in one JSON file only its owner can read.
type File struct {
	Path string
	// LockTimeout is how long to wait for another command's lock; 10s when zero.
	LockTimeout time.Duration
}

// DefaultPath is where sign-ins are kept unless LDO_TOKEN_CACHE names a file: the
// platform's state folder (~/.local/state/ldo on Linux, %LOCALAPPDATA%\ldo on Windows),
// in a file of this tool's own beside the Python ldo's.
func DefaultPath() string {
	if override := os.Getenv(FileEnv); override != "" {
		return override
	}
	var base string
	switch runtime.GOOS {
	case "windows":
		base = os.Getenv("LOCALAPPDATA")
	case "darwin":
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, "Library", "Application Support")
	default:
		base = os.Getenv("XDG_STATE_HOME")
		if base == "" {
			home, _ := os.UserHomeDir()
			base = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(base, brand.ConfigDir, brand.Command+"-sign-ins.json")
}

// Open is the store a profile's token_cache names.
func Open(cache string) (Store, error) {
	switch cache {
	case "memory":
		return NewMemory(), nil
	case "", "file", "keychain":
		// keychain falls back to the file: this build has no keychain of its own.
		return &File{Path: DefaultPath()}, nil
	}
	return nil, errs.Configf("unknown token_cache %q", cache).WithHint("use file or memory")
}

// Load is the value kept under key.
func (f *File) Load(key string) (string, bool, error) {
	data, err := f.read()
	if err != nil {
		return "", false, err
	}
	value, ok := data[key]
	return value, ok, nil
}

// Save keeps value under key, under the lock.
func (f *File) Save(key, value string) error {
	return f.changing(func(data map[string]string) bool {
		data[key] = value
		return true
	})
}

// Delete forgets key, under the lock.
func (f *File) Delete(key string) (bool, error) {
	found := false
	err := f.changing(func(data map[string]string) bool {
		_, found = data[key]
		delete(data, key)
		return found
	})
	return found, err
}

func (f *File) changing(change func(map[string]string) bool) error {
	if err := os.MkdirAll(filepath.Dir(f.Path), 0o700); err != nil {
		return errs.Authf("cannot make the folder for %s: %v", f.Path, err)
	}
	unlock, err := f.lock()
	if err != nil {
		return err
	}
	defer unlock()
	data, err := f.read()
	if err != nil {
		return err
	}
	if !change(data) {
		return nil
	}
	return f.write(data)
}

func (f *File) read() (map[string]string, error) {
	data := map[string]string{}
	info, err := os.Stat(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return data, nil
	}
	if err != nil {
		return nil, errs.Authf("cannot read %s: %v", f.Path, err)
	}
	if exposed(info) {
		return nil, errs.Authf("%s can be read by other accounts, so its sign-ins are not used", f.Path).
			WithHint("make it yours alone: chmod 600 %s", f.Path)
	}
	text, err := os.ReadFile(f.Path)
	if err != nil {
		return nil, errs.Authf("cannot read %s: %v", f.Path, err)
	}
	if len(text) > 0 && json.Unmarshal(text, &data) != nil {
		return nil, errs.Authf("%s is not a sign-in file this tool wrote", f.Path).
			WithHint("delete it, and sign in again")
	}
	return data, nil
}

func exposed(info fs.FileInfo) bool {
	// Windows has no such modes; its profile folder is the owner's alone.
	return runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0
}

func (f *File) write(data map[string]string) error {
	text, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(f.Path), "."+filepath.Base(f.Path)+".*.tmp")
	if err != nil {
		return errs.Authf("cannot write %s: %v", f.Path, err)
	}
	name := temporary.Name()
	defer os.Remove(name) // gone once renamed; removed if anything failed first
	if err := temporary.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		temporary.Close()
		return errs.Authf("cannot write %s: %v", f.Path, err)
	}
	if _, err := temporary.Write(text); err != nil {
		temporary.Close()
		return errs.Authf("cannot write %s: %v", f.Path, err)
	}
	if err := temporary.Close(); err != nil {
		return errs.Authf("cannot write %s: %v", f.Path, err)
	}
	if err := os.Rename(name, f.Path); err != nil {
		return errs.Authf("cannot write %s: %v", f.Path, err)
	}
	return nil
}

// lock takes the lock file beside the store, waiting for another command to let it go. A
// lock older than a minute was left by a command that died, and is taken over.
func (f *File) lock() (func(), error) {
	path := f.Path + ".lock"
	timeout := f.LockTimeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for {
		handle, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			// The lock file holds nothing: it is there, or it is not. A close that fails
			// leaves one that may not be, so it is removed and the lock not taken.
			if closeErr := handle.Close(); closeErr != nil {
				_ = os.Remove(path)
				return nil, errs.Authf("cannot lock %s: %v", f.Path, closeErr)
			}
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, errs.Authf("cannot lock %s: %v", f.Path, err)
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > time.Minute {
			os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, errs.Authf("another command has held %s for %s", path, timeout).
				WithHint("if none is running, delete %s", path)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
