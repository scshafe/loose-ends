package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	appconfig "loose_ends/internal/config"
)

func newConfigCommand(out io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Inspect and update Loose Ends configuration",
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}

	cmd.AddCommand(newConfigShowCommand(out))
	cmd.AddCommand(newConfigSetCommand(out))
	cmd.AddCommand(newConfigPathCommand(out))
	return cmd
}

func newConfigShowCommand(out io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "show",
		Short: "Print the active configuration",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, _, err := appconfig.Load()
			if err != nil {
				return err
			}
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(cfg)
			}

			text, err := appconfig.Marshal(cfg)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(out, text)
			return err
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	return cmd
}

func newConfigSetCommand(out io.Writer) *cobra.Command {
	return &cobra.Command{
		Use:   "set KEY VALUE",
		Short: "Set a configuration value",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, path, err := appconfig.Load()
			if err != nil {
				return err
			}

			if err := cfg.Set(args[0], args[1]); err != nil {
				return err
			}
			if err := appconfig.Save(path, cfg); err != nil {
				return err
			}

			_, err = fmt.Fprintf(out, "set %s\n", args[0])
			return err
		},
	}
}

func newConfigPathCommand(out io.Writer) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "path",
		Short: "Print the active configuration path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			path, err := appconfig.Path()
			if err != nil {
				return err
			}
			if jsonOutput {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(map[string]string{"path": path})
			}

			_, err = fmt.Fprintln(out, path)
			return err
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print JSON output")
	return cmd
}
