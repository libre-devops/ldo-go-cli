// Package snowfake is a fake ServiceNow instance: the Table API over a few tables, and
// its OAuth endpoints.
//
// It checks sign-ins as the real one does (basic, or a bearer token it issued), answers
// the encoded queries the tool sends, and runs the password, refresh token and
// authorisation code (with PKCE) grants. Approve plays the person in the browser.
package snowfake

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
)

// The instance and the account the fake knows.
const (
	Instance     = "https://dev12345.service-now.com"
	Username     = "ana"
	Password     = "p4ss-word"
	ClientID     = "0123456789abcdef0123456789abcdef"
	ClientSecret = "snow-client-secret"
	Redirect     = "http://localhost:8765/callback"
	UserID       = "6816f79cc0a8016401c5a33be04be441"
	BuildTag     = "glide-yokohama-12-18-2024__patch4-06-25-2025"
	War          = "glide-zurich-07-01-2025__patch10-05-22-2026_06-12-2026_2311.zip"
)

// closed are the tables the real instance closes to the REST API, even to admin.
var closed = map[string]bool{"v_plugin": true, "sys_plugins": true, "sys_store_app": true, "sys_package": true}

// Row is one record of a table.
type Row = map[string]any

// UserRecord is Ana's sys_user record, with overrides.
func UserRecord(overrides Row) Row {
	record := Row{
		"sys_id": UserID, "user_name": Username, "name": "Ana Analyst", "email": "ana@corp.example", "active": "true",
		"locked_out": "false", "web_service_access_only": "false", "last_login_time": "2026-09-24 08:00:00",
	}
	for key, value := range overrides {
		record[key] = value
	}
	return record
}

// Fake answers every call the ServiceNow commands make, from in-memory tables.
type Fake struct {
	mu           sync.Mutex
	Tables       map[string][]Row
	BasicAllowed bool
	Hibernating  bool
	Denied       map[string]bool
	// NoAccessToken leaves the access token out of the token endpoint's answers.
	NoAccessToken bool
	access        map[string]bool
	refresh       map[string]bool
	codes         map[string][2]string // code: challenge, redirect
	grants        []string
}

// New is an instance with Ana, her roles, the release, two applications and two tables.
func New() *Fake {
	return &Fake{
		Tables: map[string][]Row{
			"sys_user": {UserRecord(nil)},
			"sys_user_has_role": {
				{"user": UserID, "state": "active", "role.name": "itil"},
				{"user": UserID, "state": "active", "role.name": "admin"},
				{"user": UserID, "state": "inactive", "role.name": "old_role"},
			},
			// Zurich keeps the build in glide.war, not glide.buildtag.last.
			"sys_properties": {{"name": "glide.war", "value": War}},
			"sys_scope": {
				{"scope": "sn_vul", "name": "Vulnerability Response", "active": "true", "version": "18.0", "sys_class_name": "sys_store_app"},
				{"scope": "x_acme_tools", "name": "Acme tools", "active": "false", "version": "1.0.0", "sys_class_name": "sys_app"},
			},
			"sys_db_object": {{"name": "incident"}, {"name": "sys_user"}},
		},
		BasicAllowed: true, Denied: map[string]bool{}, access: map[string]bool{}, refresh: map[string]bool{},
		codes: map[string][2]string{},
	}
}

// Grants are the grant types asked of the token endpoint, in order.
func (f *Fake) Grants() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.grants...)
}

// Issued reports whether token is an access token the fake gave out.
func (f *Fake) Issued(token string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.access[token]
}

// Approve signs in at authorizeURL, as the person in the browser does, and is the address
// the browser lands on. With deny, the person refuses.
func (f *Fake) Approve(authorizeURL string, deny bool) (string, error) {
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		return "", err
	}
	query := parsed.Query()
	if parsed.Path != "/oauth_auth.do" || query.Get("client_id") != ClientID || query.Get("code_challenge_method") != "S256" {
		return "", fmt.Errorf("not an authorisation link: %s", authorizeURL)
	}
	params := url.Values{"state": {query.Get("state")}}
	if deny {
		params.Set("error", "access_denied")
	} else {
		code := randomHex()
		f.mu.Lock()
		f.codes[code] = [2]string{query.Get("code_challenge"), query.Get("redirect_uri")}
		f.mu.Unlock()
		params.Set("code", code)
	}
	return query.Get("redirect_uri") + "?" + params.Encode(), nil
}

// Handler answers one request.
func (f *Fake) Handler(request *http.Request) httpfake.Reply {
	if f.Hibernating {
		return httpfake.Reply{Status: 200, Body: httpfake.Text{Content: "<html><body>Your instance is hibernating</body></html>", ContentType: "text/html"}}
	}
	switch {
	case request.URL.Path == "/oauth_token.do":
		_ = request.ParseForm()
		return f.token(request.PostForm)
	case strings.HasPrefix(request.URL.Path, "/api/now/table/"):
		return f.table(request, strings.TrimPrefix(request.URL.Path, "/api/now/table/"))
	}
	return httpfake.Status(404, Row{"error": Row{"message": "unexpected request " + request.URL.String()}})
}

func (f *Fake) authenticated(request *http.Request) bool {
	header := request.Header.Get("Authorization")
	if encoded, ok := strings.CutPrefix(header, "Basic "); ok {
		return f.BasicAllowed && encoded == base64.StdEncoding.EncodeToString([]byte(Username+":"+Password))
	}
	token, ok := strings.CutPrefix(header, "Bearer ")
	return ok && f.Issued(token)
}

func failure(status int, message, detail string) httpfake.Reply {
	return httpfake.Status(status, Row{"error": Row{"message": message, "detail": detail}, "status": "failure"})
}

func (f *Fake) table(request *http.Request, name string) httpfake.Reply {
	if !f.authenticated(request) {
		reply := failure(401, "User is not authenticated", "Required to provide Auth information")
		reply.Headers = map[string]string{"WWW-Authenticate": `Basic realm="Service-now"`}
		return reply
	}
	if f.Denied[name] || closed[name] {
		return failure(403, "User Not Authorized", "Failed API level ACL Validation")
	}
	rows, ok := f.Tables[name]
	if !ok {
		return httpfake.Status(400, Row{"error": Row{"message": "Invalid table " + name}, "status": "failure"})
	}
	params := request.URL.Query()
	var matched []Row
	for _, row := range rows {
		if matches(row, params.Get("sysparm_query")) {
			matched = append(matched, row)
		}
	}
	offset, _ := strconv.Atoi(params.Get("sysparm_offset"))
	limit, err := strconv.Atoi(params.Get("sysparm_limit"))
	if err != nil {
		limit = 10000
	}
	page := []Row{}
	for index := offset; index < len(matched) && index < offset+limit; index++ {
		page = append(page, pick(matched[index], params.Get("sysparm_fields")))
	}
	return httpfake.Reply{Status: 200, Body: Row{"result": page}, Headers: map[string]string{"X-Total-Count": strconv.Itoa(len(matched))}}
}

// pick is row with only the fields asked for (all of them when none are).
func pick(row Row, wanted string) Row {
	if wanted == "" {
		return row
	}
	picked := Row{}
	for _, key := range strings.Split(wanted, ",") {
		if value, ok := row[key]; ok {
			picked[key] = value
		}
	}
	return picked
}

func (f *Fake) token(form url.Values) httpfake.Reply {
	grant := form.Get("grant_type")
	f.mu.Lock()
	f.grants = append(f.grants, grant)
	f.mu.Unlock()
	if form.Get("client_id") != ClientID || form.Get("client_secret") != ClientSecret {
		return httpfake.Status(401, Row{"error": "invalid_client", "error_description": "invalid_client"})
	}
	if refused := f.refused(grant, form); refused != "" {
		status := 401
		if refused == "unsupported_grant_type" {
			status = 400
		}
		return httpfake.Status(status, Row{"error": refused, "error_description": refused})
	}
	access, refresh := "access-"+randomHex(), "refresh-"+randomHex()
	f.mu.Lock()
	f.access[access], f.refresh[refresh] = true, true
	f.mu.Unlock()
	body := Row{"access_token": access, "refresh_token": refresh, "scope": "useraccount", "token_type": "Bearer", "expires_in": 1799}
	if f.NoAccessToken {
		delete(body, "access_token")
	}
	return httpfake.JSON(body)
}

// refused is the error a grant is refused with, or "".
func (f *Fake) refused(grant string, form url.Values) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch grant {
	case "password":
		if form.Get("username") != Username || form.Get("password") != Password {
			return "access_denied"
		}
	case "refresh_token":
		if !f.refresh[form.Get("refresh_token")] {
			return "invalid_grant"
		}
	case "authorization_code":
		issued := f.codes[form.Get("code")]
		delete(f.codes, form.Get("code"))
		digest := sha256.Sum256([]byte(form.Get("code_verifier")))
		if issued[0] == "" || base64.RawURLEncoding.EncodeToString(digest[:]) != issued[0] || form.Get("redirect_uri") != issued[1] {
			return "invalid_grant"
		}
	default:
		return "unsupported_grant_type"
	}
	return ""
}

// matches is a little of ServiceNow's encoded query language: =, IN, LIKE, ^ (and) and
// ^OR (or).
func matches(row Row, query string) bool {
	if query == "" {
		return true
	}
	var groups [][]string
	for _, term := range strings.Split(query, "^") {
		if rest, ok := strings.CutPrefix(term, "OR"); ok && len(groups) > 0 {
			groups[len(groups)-1] = append(groups[len(groups)-1], rest)
		} else {
			groups = append(groups, []string{term})
		}
	}
	for _, group := range groups {
		any := false
		for _, term := range group {
			any = any || matchesTerm(row, term)
		}
		if !any {
			return false
		}
	}
	return true
}

func matchesTerm(row Row, term string) bool {
	if field, values, ok := strings.Cut(term, "IN"); ok && !strings.Contains(field, "=") {
		for _, value := range strings.Split(values, ",") {
			if text(row, field) == value {
				return true
			}
		}
		return false
	}
	if field, value, ok := strings.Cut(term, "LIKE"); ok {
		return strings.Contains(strings.ToLower(text(row, field)), strings.ToLower(value))
	}
	field, value, _ := strings.Cut(term, "=")
	if value == "javascript:gs.getUserID()" {
		value = UserID
	}
	return text(row, field) == value
}

// text is a field as text, "" when the row has none.
func text(row Row, field string) string {
	if value, ok := row[field]; ok {
		return fmt.Sprint(value)
	}
	return ""
}

func randomHex() string {
	data := make([]byte, 6)
	_, _ = rand.Read(data)
	return hex.EncodeToString(data)
}
