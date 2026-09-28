// Package errs holds the errors this tool raises. Library code returns them and never
// exits; only the CLI turns them into messages and exit codes, so a library caller keeps
// control of its own flow.
//
// Each error has a Kind, a message, and a Hint: the next step for the person running
// the command. Match one with errors.As, or test its kind with Is(err, errs.NotFound).
package errs

import (
	"errors"
	"fmt"
)

// Kind is what went wrong, broadly: it decides how the CLI reports the error.
type Kind int

const (
	// Input is something given that cannot be used: a name, an id, a query, a duration.
	Input Kind = iota + 1
	// Config is a config file that is invalid, or a profile that cannot do this.
	Config
	// ConfigNotFound is a config file that does not exist.
	ConfigNotFound
	// Command is an external tool that is missing, or a command it ran that failed.
	Command
	// Auth is a credential that could not produce an access token.
	Auth
	// Reauth is a sign-in that has lapsed: only a person signing in again renews it.
	Reauth
	// Token is a token that could not be decoded.
	Token
	// NotFound is a named object that does not exist.
	NotFound
	// Ambiguous is a name that matched more than one object where one was needed.
	Ambiguous
	// API is an HTTP API call that failed.
	API
)

var kindNames = map[Kind]string{
	Input: "input", Config: "config", ConfigNotFound: "config not found", Command: "command",
	Auth: "auth", Reauth: "sign-in lapsed", Token: "token", NotFound: "not found",
	Ambiguous: "ambiguous", API: "api",
}

func (k Kind) String() string {
	if name, ok := kindNames[k]; ok {
		return name
	}
	return "error"
}

// Error is every error this tool raises.
type Error struct {
	Kind    Kind
	Message string
	// Hint is the next step for the person, or empty.
	Hint string
	// For API errors: the HTTP status, the service's error code and its request id.
	Status    int
	Code      string
	RequestID string
	// For a lapsed sign-in: the tenant, and why in plain words.
	TenantID string
	Reason   string
}

func (e *Error) Error() string { return e.Message }

// WithHint is the error with a hint to go with it.
func (e *Error) WithHint(format string, args ...any) *Error {
	e.Hint = fmt.Sprintf(format, args...)
	return e
}

func newError(kind Kind, format string, args []any) *Error {
	return &Error{Kind: kind, Message: fmt.Sprintf(format, args...)}
}

// Inputf is an Input error.
func Inputf(format string, args ...any) *Error { return newError(Input, format, args) }

// Configf is a Config error.
func Configf(format string, args ...any) *Error { return newError(Config, format, args) }

// ConfigNotFoundf is a ConfigNotFound error.
func ConfigNotFoundf(format string, args ...any) *Error {
	return newError(ConfigNotFound, format, args)
}

// Commandf is a Command error.
func Commandf(format string, args ...any) *Error { return newError(Command, format, args) }

// Authf is an Auth error.
func Authf(format string, args ...any) *Error { return newError(Auth, format, args) }

// Tokenf is a Token error.
func Tokenf(format string, args ...any) *Error { return newError(Token, format, args) }

// NotFoundf is a NotFound error.
func NotFoundf(format string, args ...any) *Error { return newError(NotFound, format, args) }

// Ambiguousf is an Ambiguous error.
func Ambiguousf(format string, args ...any) *Error { return newError(Ambiguous, format, args) }

// APIf is an API error, its status, code and request id set by the caller when known.
func APIf(format string, args ...any) *Error { return newError(API, format, args) }

// Reauthf is a lapsed sign-in to a tenant, with the reason.
func Reauthf(tenantID, reason, format string, args ...any) *Error {
	err := newError(Reauth, format, args)
	err.TenantID, err.Reason = tenantID, reason
	return err
}

// As is the *Error inside err, or nil.
func As(err error) *Error {
	var found *Error
	if errors.As(err, &found) {
		return found
	}
	return nil
}

// Is reports whether err is one of this package's errors of the given kind. An Auth
// check matches a lapsed sign-in too, as a lapse is one way a credential fails.
func Is(err error, kind Kind) bool {
	found := As(err)
	if found == nil {
		return false
	}
	return found.Kind == kind || (kind == Auth && found.Kind == Reauth) ||
		(kind == Config && found.Kind == ConfigNotFound)
}

// HintOf is err's hint, or empty.
func HintOf(err error) string {
	if found := As(err); found != nil {
		return found.Hint
	}
	return ""
}
