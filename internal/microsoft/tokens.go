package microsoft

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/core/util"
)

// A token is decoded here to check it is the token you meant to get. Only the claims are
// read; the signature is NOT verified. The checks answer "is this token for the right API
// and tenant, in date, with the permissions this tool needs?", not "is this token
// genuine?" (Graph tokens cannot be signature-checked by a client in any case).

// DecodedToken is the header and claims of a JWT.
type DecodedToken struct {
	Header map[string]any
	Claims map[string]any
}

// Timestamp is a NumericDate claim (exp, nbf, iat) as a UTC time, or the zero time.
func (d DecodedToken) Timestamp(claim string) time.Time {
	if value, ok := d.Claims[claim].(float64); ok {
		return time.Unix(int64(value), 0).UTC()
	}
	return time.Time{}
}

// ExpiresAt is when the token expires (exp).
func (d DecodedToken) ExpiresAt() time.Time { return d.Timestamp("exp") }

// Audiences are who the token is for (aud): one or several.
func (d DecodedToken) Audiences() []string {
	switch aud := d.Claims["aud"].(type) {
	case string:
		return []string{aud}
	case []any:
		var found []string
		for _, item := range aud {
			found = append(found, fmt.Sprint(item))
		}
		return found
	}
	return nil
}

func (d DecodedToken) text(claim string) string {
	if value, ok := d.Claims[claim].(string); ok {
		return value
	}
	return ""
}

// TenantID is the tenant that issued the token (tid).
func (d DecodedToken) TenantID() string { return d.text("tid") }

// Issuer is the token's issuer URL (iss).
func (d DecodedToken) Issuer() string { return d.text("iss") }

// Scopes are the delegated permissions (scp).
func (d DecodedToken) Scopes() []string { return strings.Fields(d.text("scp")) }

// Roles are the application permissions (roles).
func (d DecodedToken) Roles() []string {
	var found []string
	for _, role := range asList(d.Claims["roles"]) {
		found = append(found, fmt.Sprint(role))
	}
	return found
}

func asList(value any) []any {
	if list, ok := value.([]any); ok {
		return list
	}
	return nil
}

// Principal is who the token is for: a user name for a delegated token, else an app id.
func (d DecodedToken) Principal() string {
	for _, claim := range []string{"upn", "unique_name", "preferred_username", "appid", "azp", "oid"} {
		if value := d.text(claim); value != "" {
			return value
		}
	}
	return ""
}

// AppID is the client application that asked for the token.
func (d DecodedToken) AppID() string {
	if value := d.text("appid"); value != "" {
		return value
	}
	return d.text("azp")
}

// IdentityType is user (delegated), app (application) or unknown.
func (d DecodedToken) IdentityType() string {
	if value := d.text("idtyp"); value != "" {
		return value
	}
	if len(d.Scopes()) > 0 {
		return "user"
	}
	if len(d.Roles()) > 0 {
		return "app"
	}
	return "unknown"
}

// DecodeToken decodes a JWT without verifying it. A Bearer prefix is allowed.
func DecodeToken(token string) (DecodedToken, error) {
	raw := strings.TrimSpace(token)
	if len(raw) > 7 && strings.EqualFold(raw[:7], "bearer ") {
		raw = strings.TrimSpace(raw[7:])
	}
	parts := strings.Split(raw, ".")
	if len(parts) == 5 {
		return DecodedToken{}, errs.Tokenf("this is an encrypted token (JWE); its claims cannot be read")
	}
	if len(parts) != 3 {
		return DecodedToken{}, errs.Tokenf("not a JWT: expected three dot-separated parts")
	}
	header, err := segment(parts[0], "header")
	if err != nil {
		return DecodedToken{}, err
	}
	claims, err := segment(parts[1], "payload")
	if err != nil {
		return DecodedToken{}, err
	}
	return DecodedToken{Header: header, Claims: claims}, nil
}

func segment(value, name string) (map[string]any, error) {
	data, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	if err != nil {
		return nil, errs.Tokenf("cannot decode the token %s: %v", name, err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, errs.Tokenf("the token %s is not a JSON object", name)
	}
	return decoded, nil
}

// Check is the outcome of one token check: pass, warn or fail.
type Check struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// ValidateOptions are what a token is checked against, beyond its own dates and issuer.
type ValidateOptions struct {
	Resource     *Resource
	TenantID     string
	Required     []string
	Requirements []Requirement
	Now          time.Time
}

// ValidateToken checks a decoded token's claims.
//
// It always checks expiry, not-before, and that the issuer is the token's own tenant.
// With a Resource it checks the audience, and warns for each of the requirements for
// that resource the token does not cover (so it says which features the token serves).
// With a TenantID it checks the tenant. Each name in Required must be in scp or roles.
func ValidateToken(token DecodedToken, opts ValidateOptions) []Check {
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	checks := []Check{expiryCheck(token.ExpiresAt(), now)}
	if notBefore := token.Timestamp("nbf"); !notBefore.IsZero() && notBefore.Sub(now) > time.Minute {
		checks = append(checks, Check{"not-before", "fail", "not valid for " + util.FormatDuration(notBefore.Sub(now))})
	}
	if token.TenantID() != "" {
		checks = append(checks, issuerCheck(token))
	}
	if opts.TenantID != "" {
		checks = append(checks, tenantCheck(token, opts.TenantID))
	}
	granted := unique(append(token.Scopes(), token.Roles()...))
	folded := foldAll(granted)
	if opts.Resource != nil {
		checks = append(checks, audienceCheck(token, *opts.Resource))
		var relevant []Requirement
		for _, requirement := range opts.Requirements {
			if requirement.Resource == opts.Resource.Key {
				relevant = append(relevant, requirement)
			}
		}
		checks = append(checks, permissionChecks(relevant, folded, granted)...)
	}
	for _, permission := range opts.Required {
		if slices.Contains(folded, strings.ToLower(permission)) {
			checks = append(checks, Check{"requires " + permission, "pass", "present"})
		} else {
			checks = append(checks, Check{"requires " + permission, "fail", "not in scp or roles"})
		}
	}
	return checks
}

func foldAll(values []string) []string {
	folded := make([]string, len(values))
	for index, value := range values {
		folded[index] = strings.ToLower(value)
	}
	return unique(folded)
}

// unique is values without repeats, first kept.
func unique(values []string) []string {
	var kept []string
	for _, value := range values {
		if !slices.Contains(kept, value) {
			kept = append(kept, value)
		}
	}
	return kept
}

func expiryCheck(expires, now time.Time) Check {
	switch {
	case expires.IsZero():
		return Check{"expiry", "fail", "no exp claim"}
	case !expires.After(now):
		return Check{"expiry", "fail", "expired " + util.FormatDuration(now.Sub(expires)) + " ago"}
	case expires.Sub(now) <= 5*time.Minute:
		return Check{"expiry", "warn", "expires in " + util.FormatDuration(expires.Sub(now))}
	}
	return Check{"expiry", "pass", "expires in " + util.FormatDuration(expires.Sub(now))}
}

func issuerCheck(token DecodedToken) Check {
	// The issuer names the token's own tenant, as Entra's always does.
	if strings.Contains(strings.ToLower(token.Issuer()), strings.ToLower(token.TenantID())) {
		return Check{"issuer", "pass", token.Issuer()}
	}
	return Check{"issuer", "fail", fmt.Sprintf("%q does not match tid %s", token.Issuer(), token.TenantID())}
}

func tenantCheck(token DecodedToken, tenantID string) Check {
	if strings.EqualFold(token.TenantID(), tenantID) {
		return Check{"tenant", "pass", token.TenantID()}
	}
	found := token.TenantID()
	if found == "" {
		found = "(none)"
	}
	return Check{"tenant", "fail", "tid " + found + ", expected " + tenantID}
}

func audienceCheck(token DecodedToken, resource Resource) Check {
	audiences := token.Audiences()
	for _, aud := range audiences {
		if resource.Accepts(aud) {
			return Check{"audience", "pass", strings.Join(audiences, ", ") + " is " + resource.Key}
		}
	}
	shown := strings.Join(audiences, ", ")
	if shown == "" {
		shown = "(none)"
	}
	return Check{"audience", "fail", shown + " is not " + resource.Key + " (" + resource.URL + ")"}
}

func permissionChecks(requirements []Requirement, granted, names []string) []Check {
	if len(requirements) == 0 {
		return nil
	}
	if len(granted) == 0 {
		return []Check{{"permissions", "warn", "no scp or roles claim; access then rests on the service's own RBAC"}}
	}
	if len(granted) == 1 && granted[0] == "user_impersonation" {
		// A delegated grant with no granular scopes, as the Azure CLI gets for Defender:
		// what the user can do is decided by their role in the service, not the token.
		return []Check{{"permissions", "warn", "only user_impersonation; access then rests on your role in the service"}}
	}
	byName := map[string]string{}
	for _, name := range names {
		byName[strings.ToLower(name)] = name
	}
	var checks []Check
	for _, requirement := range requirements {
		var via []string
		var missing []string
		for _, group := range requirement.AllOf {
			match := ""
			for _, permission := range group {
				if slices.Contains(granted, strings.ToLower(permission)) {
					match = byName[strings.ToLower(permission)]
					break
				}
			}
			if match == "" {
				missing = append(missing, "one of "+strings.Join(group, ", "))
			} else {
				via = append(via, match)
			}
		}
		name := "permissions: " + requirement.Feature
		if len(missing) > 0 {
			checks = append(checks, Check{name, "warn", "needs " + strings.Join(missing, "; ")})
		} else {
			checks = append(checks, Check{name, "pass", "via " + strings.Join(unique(via), ", ")})
		}
	}
	return checks
}

// Passed reports whether no check failed (and, with strict, none warned).
func Passed(checks []Check, strict bool) bool {
	for _, check := range checks {
		if check.Status == "fail" || (strict && check.Status == "warn") {
			return false
		}
	}
	return true
}
