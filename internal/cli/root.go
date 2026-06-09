package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

var noColor bool

func Execute() error {
	return NewRootCommand(os.Stdout, os.Stderr).Execute()
}

func NewRootCommand(out io.Writer, errOut io.Writer) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "loose-ends",
		Short:         "CLI-first personal task manager",
		SilenceUsage:  true,
		SilenceErrors: true,
		Run: func(cmd *cobra.Command, args []string) {
			_ = cmd.Help()
		},
	}

	cmd.SetOut(out)
	cmd.SetErr(errOut)
	cmd.PersistentFlags().BoolVar(&noColor, "no-color", false, "disable colored output")
	cmd.AddCommand(newAddCommand(out))
	cmd.AddCommand(newListCommand(out))
	cmd.AddCommand(newShowCommand(out))
	cmd.AddCommand(newEditCommand(out))
	cmd.AddCommand(newStatusCommand(out, "done", "Mark a task done", "done"))
	cmd.AddCommand(newStatusCommand(out, "partial", "Mark a task partially complete", "partially_complete"))
	cmd.AddCommand(newStatusCommand(out, "archive", "Archive a task", "archived"))
	cmd.AddCommand(newDuplicateCommand(out))
	cmd.AddCommand(newStatusCommand(out, "reopen", "Reopen a task", "open"))
	cmd.AddCommand(newRemoveCommand(out))
	cmd.AddCommand(newMoveCommand(out))
	cmd.AddCommand(newRebalanceCommand(out))
	cmd.AddCommand(newTagCommand(out))
	cmd.AddCommand(newUntagCommand(out))
	cmd.AddCommand(newTagsCommand(out))
	cmd.AddCommand(newConfigCommand(out))
	cmd.AddCommand(newServeCommand(out))
	cmd.AddCommand(newMigrateCommand(out))
	cmd.SetFlagErrorFunc(func(cmd *cobra.Command, err error) error {
		return fmt.Errorf("%w\n\nRun 'loose-ends --help' for usage", err)
	})

	return cmd
}
