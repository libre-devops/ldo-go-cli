package webbrowser

import (
	"errors"
	"testing"
)

func TestABrowserCanBeOpenedWhereThereIsOne(t *testing.T) {
	env := func(values map[string]string) func(string) string {
		return func(name string) string { return values[name] }
	}
	has := func(names ...string) func(string) (string, error) {
		return func(name string) (string, error) {
			for _, found := range names {
				if found == name {
					return "/usr/bin/" + name, nil
				}
			}
			return "", errors.New("not found")
		}
	}
	for _, test := range []struct {
		system string
		env    map[string]string
		tools  []string
		want   bool
	}{
		{"windows", nil, nil, true},
		{"darwin", nil, nil, true},
		{"linux", nil, []string{"xdg-open"}, false},
		{"linux", map[string]string{"BROWSER": "firefox"}, nil, true},
		{"linux", map[string]string{"DISPLAY": ":0"}, []string{"xdg-open"}, true},
		{"linux", map[string]string{"WAYLAND_DISPLAY": "wayland-0"}, nil, false},
		{"linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, []string{"wslview"}, true},
		{"linux", map[string]string{"WSL_DISTRO_NAME": "Ubuntu"}, nil, false},
	} {
		if got := canLaunch(test.system, env(test.env), has(test.tools...)); got != test.want {
			t.Errorf("%s %v %v: %v", test.system, test.env, test.tools, got)
		}
	}
	_ = CanLaunch(env(nil), has())
}
