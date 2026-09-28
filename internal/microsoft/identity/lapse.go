package identity

import (
	"regexp"
	"strings"
)

// An access token lasts about an hour, and a refresh token renews it without asking
// anyone. These Entra ID errors mean the refresh token itself can no longer be used, so
// retrying cannot help: someone has to sign in again. Naming the cause says whether that
// will keep happening (a sign-in frequency policy) or was a one-off (a password change).
var lapseReasons = map[string]string{
	"AADSTS700082": "the session went unused for too long, so its refresh token expired",
	"AADSTS70043":  "a Conditional Access sign-in frequency policy wants a fresh sign-in",
	"AADSTS70044":  "a Conditional Access sign-in frequency policy wants a fresh sign-in",
	"AADSTS50173":  "the session was revoked (by an administrator, or by signing out everywhere)",
	"AADSTS50132":  "a password change or expiry ended the session",
	"AADSTS50133":  "a password change or expiry ended the session",
	"AADSTS50055":  "the password has expired",
	"AADSTS50076":  "multi-factor authentication is now required",
	"AADSTS50078":  "the multi-factor authentication on the session has expired",
	"AADSTS50079":  "multi-factor authentication must be set up for this account",
	"AADSTS50158":  "an external security challenge was not satisfied",
}

var aadsts = regexp.MustCompile(`AADSTS\d+`)

// LapseReason is the cause when message says a sign-in lapsed, else empty. It knows the
// Entra ID codes above, and the Azure CLI's own ways of saying it has no usable session
// (its messages all tell you to run az login).
func LapseReason(message string) string {
	for _, code := range aadsts.FindAllString(message, -1) {
		if reason, ok := lapseReasons[code]; ok {
			return reason
		}
	}
	if strings.Contains(message, "az login") {
		return "the Azure CLI has no usable sign-in for this tenant"
	}
	return ""
}
