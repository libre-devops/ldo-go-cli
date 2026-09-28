package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/libre-devops/ldo-go-cli/internal/core/brand"
)

// welcomeCommand is welcome: the banner, the version, the config file, and the next step.
func welcomeCommand(rt *Runtime) *cobra.Command {
	return &cobra.Command{
		Use:   "welcome",
		Short: "Say hello: the banner, the version, the config file, and the next step.",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			showBanner(rt, true)
			file, err := rt.OptionalConfigFile()
			if err != nil {
				return err
			}
			path := rt.ConfigPathInUse()
			if file == nil {
				path += " (not created yet)"
			}
			rt.Console.Println(pairs(rt, [][2]string{{"Version", brand.Command + " " + brand.Version}, {"Config", path}}))
			rt.Console.Println("")
			steps := [][2]string{{"config init", "write a config file to fill in"}, {"--help", "see every command"}}
			if file != nil {
				steps = [][2]string{{"profiles", "check your profiles and sign-ins"},
					{"devices check --help", "check devices across Entra and Defender"}, {"--help", "see every command"}}
			}
			for index, step := range steps {
				steps[index][0] = strings.Trim(brand.Suggest(step[0]), "'")
			}
			rt.Console.Println(title(rt, "Next"))
			rt.Console.Println(pairs(rt, steps))
			return nil
		},
	}
}
