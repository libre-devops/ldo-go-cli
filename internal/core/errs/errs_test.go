package errs

import (
	"errors"
	"fmt"
	"testing"
)

func TestKindsAndHints(t *testing.T) {
	err := NotFoundf("no device %q", "web01").WithHint("check the name")
	wrapped := fmt.Errorf("looking: %w", err)
	if !Is(wrapped, NotFound) || Is(wrapped, Input) || HintOf(wrapped) != "check the name" {
		t.Fatal("kind or hint lost when wrapped")
	}
	if err.Error() != `no device "web01"` || NotFound.String() != "not found" {
		t.Fatal(err.Error())
	}
	lapsed := Reauthf("tenant", "expired", "signed out")
	if !Is(lapsed, Auth) || As(lapsed).TenantID != "tenant" {
		t.Fatal("a lapse is an auth error")
	}
	if !Is(ConfigNotFoundf("x"), Config) || Is(errors.New("plain"), Config) || HintOf(nil) != "" {
		t.Fatal("config kinds")
	}
}
