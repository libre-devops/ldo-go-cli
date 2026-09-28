package brand

import (
	"runtime/debug"
	"strings"
	"testing"
)

func TestNamesComeFromTheBrand(t *testing.T) {
	if EnvVar("PROFILE") != EnvPrefix+"_PROFILE" || ProfileEnv != EnvPrefix+"_PROFILE" {
		t.Error(EnvVar("PROFILE"))
	}
	if Suggest("config init") != "'"+Command+" config init'" {
		t.Error(Suggest("config init"))
	}
	if !strings.HasPrefix(Docs("graph"), Repository) || !strings.HasSuffix(Docs("graph"), "graph.md") {
		t.Error(Docs("graph"))
	}
	if !strings.Contains(Banner, "\n") {
		t.Error("no banner")
	}
}

func TestAnInstalledModuleKnowsItsVersion(t *testing.T) {
	built := func(version string) func() (*debug.BuildInfo, bool) {
		return func() (*debug.BuildInfo, bool) { return &debug.BuildInfo{Main: debug.Module{Version: version}}, true }
	}
	for _, test := range []struct {
		set, module, want string
	}{
		{"0.2.0", "v0.1.0", "0.2.0"},
		{"", "v0.1.0", "0.1.0"},
		{"", "(devel)", developmentVersion},
		{"", "", developmentVersion},
		{"", "v0.0.0-20260928014200-abcdef123456", developmentVersion},
		{"", "v0.1.1-0.20260928014200-abcdef123456+dirty", developmentVersion},
		{"", "v0.2.0-rc.1", "0.2.0-rc.1"},
	} {
		if got := versionOf(test.set, built(test.module)); got != test.want {
			t.Errorf("%q, %s: %s", test.set, test.module, got)
		}
	}
	if got := versionOf("", func() (*debug.BuildInfo, bool) { return nil, false }); got != developmentVersion {
		t.Error(got)
	}
}
