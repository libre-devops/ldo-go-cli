package tokenstore

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
)

func TestMemoryKeepsValuesForTheRun(t *testing.T) {
	store := NewMemory()
	if _, found, _ := store.Load("k"); found {
		t.Error("empty store has a value")
	}
	_ = store.Save("k", "v")
	if value, found, _ := store.Load("k"); !found || value != "v" {
		t.Errorf("%q %v", value, found)
	}
	if gone, _ := store.Delete("k"); !gone {
		t.Error("nothing deleted")
	}
	if gone, _ := store.Delete("k"); gone {
		t.Error("deleted twice")
	}
}

func TestAFileIsYoursAlone(t *testing.T) {
	store := &File{Path: filepath.Join(t.TempDir(), "state", "sign-ins.json")}
	if err := store.Save("msal:app", `{"a":1}`); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(store.Path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", info.Mode().Perm())
	}
	if value, found, _ := store.Load("msal:app"); !found || value != `{"a":1}` {
		t.Errorf("%q", value)
	}
	if gone, err := store.Delete("msal:app"); !gone || err != nil {
		t.Errorf("%v %v", gone, err)
	}
	if gone, _ := store.Delete("msal:app"); gone {
		t.Error("deleted twice")
	}
}

func TestAFileOthersCanReadIsRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no such modes on Windows")
	}
	path := filepath.Join(t.TempDir(), "sign-ins.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := (&File{Path: path}).Load("k")
	if !errs.Is(err, errs.Auth) || !strings.Contains(errs.HintOf(err), "chmod 600") {
		t.Errorf("%v", err)
	}
}

func TestAFileThatIsNotOursIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sign-ins.json")
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := (&File{Path: path}).Load("k"); !errs.Is(err, errs.Auth) {
		t.Errorf("%v", err)
	}
}

func TestTwoCommandsAtOnceKeepBothSignIns(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sign-ins.json")
	var group sync.WaitGroup
	for index := range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := (&File{Path: path}).Save(string(rune('a'+index)), "v"); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	for index := range 8 {
		if _, found, _ := (&File{Path: path}).Load(string(rune('a' + index))); !found {
			t.Errorf("lost %c", 'a'+index)
		}
	}
}

func TestAHeldLockTimesOutAndAStaleOneIsTakenOver(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sign-ins.json")
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	err := (&File{Path: path, LockTimeout: 60 * time.Millisecond}).Save("k", "v")
	if !errs.Is(err, errs.Auth) || !strings.Contains(err.Error(), "another command") {
		t.Errorf("%v", err)
	}
	old := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(path+".lock", old, old); err != nil {
		t.Fatal(err)
	}
	if err := (&File{Path: path}).Save("k", "v"); err != nil {
		t.Errorf("a stale lock was not taken over: %v", err)
	}
}

func TestOpenAndTheDefaultPath(t *testing.T) {
	t.Setenv(FileEnv, "")
	t.Setenv("XDG_STATE_HOME", "/state")
	if path := DefaultPath(); runtime.GOOS == "linux" && path != "/state/ldo/ldo-go-sign-ins.json" {
		t.Errorf("%s", path)
	}
	t.Setenv(FileEnv, "/elsewhere.json")
	if DefaultPath() != "/elsewhere.json" {
		t.Error(DefaultPath())
	}
	if store, _ := Open("memory"); store == nil {
		t.Error("no memory store")
	}
	if store, _ := Open("keychain"); store.(*File).Path != "/elsewhere.json" {
		t.Error("keychain should fall back to the file")
	}
	if _, err := Open("disk"); !errs.Is(err, errs.Config) {
		t.Errorf("%v", err)
	}
}
