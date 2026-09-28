package instance

import (
	"context"
	"strings"
	"testing"

	"github.com/libre-devops/ldo-go-cli/internal/core/errs"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/httpfake"
	"github.com/libre-devops/ldo-go-cli/internal/fakes/snowfake"
	"github.com/libre-devops/ldo-go-cli/internal/servicenow"
)

var ctx = context.Background()

func client(t *testing.T, instance *snowfake.Fake) (*Client, *httpfake.Transport) {
	t.Helper()
	http, transport := httpfake.Client(instance.Handler)
	basic, _ := servicenow.NewBasic(snowfake.Username, snowfake.Password)
	tables, err := servicenow.NewTables(snowfake.Instance, basic.Source(), basic.Scheme(), http)
	if err != nil {
		t.Fatal(err)
	}
	return &Client{Tables: tables}, transport
}

func TestTheCurrentUserIsWhoeverTheRequestSignsInAs(t *testing.T) {
	found, transport := client(t, snowfake.New())
	user, err := found.CurrentUser(ctx, "")
	if err != nil || user.SysID != snowfake.UserID || user.UserName != "ana" || !user.Active || user.LockedOut || user.Raw["email"] != "ana@corp.example" {
		t.Fatal(user, err)
	}
	if !strings.Contains(transport.Seen()[0].URL.RawQuery, "javascript%3Ags.getUserID%28%29") {
		t.Error(transport.Seen()[0].URL.RawQuery)
	}
}

func TestANamedUserMustExist(t *testing.T) {
	found, _ := client(t, snowfake.New())
	if _, err := found.CurrentUser(ctx, "bob"); !errs.Is(err, errs.NotFound) || !strings.Contains(err.Error(), "named 'bob'") {
		t.Error(err)
	}
	if user, err := found.CurrentUser(ctx, "ana"); err != nil || user.Name != "Ana Analyst" {
		t.Error(user, err)
	}
	if _, err := found.CurrentUser(ctx, "ana^ORactive=true"); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
	instance := snowfake.New()
	instance.Tables["sys_user"] = nil
	empty, _ := client(t, instance)
	if _, err := empty.CurrentUser(ctx, ""); err == nil || !strings.Contains(err.Error(), "for this sign-in") {
		t.Error(err)
	}
}

func TestRolesAreTheActiveOnesSorted(t *testing.T) {
	found, _ := client(t, snowfake.New())
	user, _ := found.CurrentUser(ctx, "")
	if roles, err := found.Roles(ctx, user); err != nil || strings.Join(roles, ",") != "admin,itil" {
		t.Error(roles, err)
	}
}

func TestTheReleaseComesFromGlideWarOrTheBuildTag(t *testing.T) {
	instance := snowfake.New()
	found, _ := client(t, instance)
	release, ok, err := found.Release(ctx)
	if err != nil || !ok || release.Family != "Zurich" || release.Patch != "patch10" || release.Label() != "Zurich patch10" || release.BuildDate != "07-01-2025" {
		t.Fatal(release, err)
	}
	instance.Tables["sys_properties"] = append(instance.Tables["sys_properties"], snowfake.Row{"name": "glide.buildtag.last", "value": snowfake.BuildTag})
	// The build tag wins.
	if release, _, _ := found.Release(ctx); release.Label() != "Yokohama patch4" {
		t.Error(release.Label())
	}
	instance.Tables["sys_properties"] = nil
	if _, ok, err := found.Release(ctx); ok || err != nil {
		t.Error(ok, err)
	}
	instance.Denied["sys_properties"] = true
	if _, ok, err := found.Release(ctx); ok || err != nil {
		t.Error(ok, err)
	}
	instance.Hibernating = true
	if _, _, err := found.Release(ctx); err == nil {
		t.Error("a hibernating instance answered")
	}
}

func TestAnUnrecognisedBuildTagIsKeptAsItIs(t *testing.T) {
	if label := ReleaseFrom("custom-build").Label(); label != "custom-build" {
		t.Error(label)
	}
	if label := ReleaseFrom("").Label(); label != "unknown" {
		t.Error(label)
	}
	if release := ReleaseFrom("glide-washingtondc-01-02-2024"); release.Label() != "Washingtondc" {
		t.Error(release.Label())
	}
}

func TestApplicationsAreListedActiveFirstAndSearched(t *testing.T) {
	found, _ := client(t, snowfake.New())
	active, _ := found.Applications(ctx, "", true)
	if len(active) != 1 || active[0].Name != "Vulnerability Response" || active[0].Kind != "store app" {
		t.Error(active)
	}
	every, _ := found.Applications(ctx, "", false)
	if len(every) != 2 || every[0].Scope != "sn_vul" || every[1].Scope != "x_acme_tools" || every[1].Active || every[1].Kind != "custom app" {
		t.Error(every)
	}
	if searched, _ := found.Applications(ctx, "ACME", false); len(searched) != 1 || searched[0].Scope != "x_acme_tools" {
		t.Error(searched)
	}
	if _, err := found.Applications(ctx, "a^b", false); !errs.Is(err, errs.Input) {
		t.Error(err)
	}
}

func TestSecurityIncidentResponseIsInstalledWhenItsTableIsThere(t *testing.T) {
	instance := snowfake.New()
	found, _ := client(t, instance)
	status, err := found.SecurityIncidentResponse(ctx)
	if err != nil || status.Installed || !strings.Contains(status.Detail, "sn_si_incident") {
		t.Fatal(status, err)
	}
	instance.Tables["sys_db_object"] = append(instance.Tables["sys_db_object"], snowfake.Row{"name": "sn_si_incident"})
	if status, _ := found.SecurityIncidentResponse(ctx); !status.Installed || status.Detail != "table sn_si_incident" {
		t.Error(status)
	}
	instance.Tables["sys_scope"] = append(instance.Tables["sys_scope"], snowfake.Row{"scope": "sn_si", "name": "Security Incident Response", "active": "true", "version": "21.2"})
	if status, _ := found.SecurityIncidentResponse(ctx); !status.Installed || status.Version != "21.2" {
		t.Error(status)
	}
	delete(instance.Tables, "sys_scope")
	if _, err := found.SecurityIncidentResponse(ctx); err == nil {
		t.Error("a missing sys_scope was taken")
	}
}
