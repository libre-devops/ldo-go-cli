// Package instance is the ServiceNow instance itself: the signed-in user and roles, the
// release, and applications.
package instance

import (
	"regexp"
	"strings"

	"github.com/libre-devops/ldo-go-cli/internal/core/fields"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow"
)

// Requirements are the roles this feature's calls need: reading applications and system
// properties takes admin on a default instance.
var Requirements = []servicenow.RoleRequirement{{Feature: "instance release and applications", AnyOf: []string{"admin"}}}

// glide-yokohama-12-18-2024__patch4-06-25-2025 (glide.buildtag.last), or the same with a
// .zip suffix (glide.war), for example.
var buildTag = regexp.MustCompile(`^glide-([a-z]+)-(\d{2}-\d{2}-\d{4})(?:__([a-z0-9]+))?`)

func flag(value any) bool {
	switch strings.ToLower(strings.TrimSpace(fields.String(value))) {
	case "true", "1", "active", "yes":
		return true
	}
	return false
}

// User is a sys_user record.
type User struct {
	SysID          string
	UserName       string
	Name           string
	Email          string
	Active         bool
	LockedOut      bool
	WebServiceOnly bool
	LastLogin      string
	Raw            servicenow.Record
}

// UserFields are the fields a User is read from.
var UserFields = []string{"sys_id", "user_name", "name", "email", "active", "locked_out", "web_service_access_only", "last_login_time"}

// UserFrom is a user as the Table API returns it.
func UserFrom(record servicenow.Record) User {
	return User{
		SysID: fields.Text(record, "sys_id"), UserName: fields.Text(record, "user_name"), Name: fields.Text(record, "name"),
		Email: fields.Text(record, "email"), Active: flag(record["active"]), LockedOut: flag(record["locked_out"]),
		WebServiceOnly: flag(record["web_service_access_only"]), LastLogin: fields.Text(record, "last_login_time"), Raw: record,
	}
}

// Release is the instance's release, from its build tag: family, build date and patch.
type Release struct {
	BuildTag  string
	Family    string
	BuildDate string
	Patch     string
}

// ReleaseFrom is a release from a build tag such as glide-zurich-07-01-2025__patch4; a
// tag of another shape is kept as it is.
func ReleaseFrom(tag string) Release {
	match := buildTag.FindStringSubmatch(strings.TrimSpace(tag))
	if match == nil {
		return Release{BuildTag: tag}
	}
	return Release{BuildTag: tag, Family: strings.ToUpper(match[1][:1]) + match[1][1:], BuildDate: match[2], Patch: match[3]}
}

// Label is the release as people say it (Zurich patch4), else its build tag.
func (r Release) Label() string {
	if r.Family == "" {
		if r.BuildTag == "" {
			return "unknown"
		}
		return r.BuildTag
	}
	return strings.TrimSpace(r.Family + " " + r.Patch)
}

// Application is a scoped application (sys_scope): from the ServiceNow Store, or built
// here.
//
// The plugin tables (v_plugin, sys_plugins, sys_store_app) refuse the REST API even to
// admin, but every scoped application, Security Incident Response (sn_si) among them, is
// listed in sys_scope, which it may read.
type Application struct {
	Scope   string
	Name    string
	Active  bool
	Version string
	// Kind is "store app" or "custom app".
	Kind string
	Raw  servicenow.Record
}

// ApplicationFields are the fields an Application is read from.
var ApplicationFields = []string{"scope", "name", "active", "version", "sys_class_name"}

// ApplicationFrom is an installed application as the Table API returns it.
func ApplicationFrom(record servicenow.Record) Application {
	kind := "custom app"
	if record["sys_class_name"] == "sys_store_app" {
		kind = "store app"
	}
	return Application{Scope: fields.Text(record, "scope"), Name: fields.Text(record, "name"), Active: flag(record["active"]),
		Version: fields.Text(record, "version"), Kind: kind, Raw: record}
}

// AppStatus is whether an application is on the instance, and how we know.
type AppStatus struct {
	Name      string
	Installed bool
	Version   string
	Detail    string
}
