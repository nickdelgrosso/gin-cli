package gincmd

import (
	ginclient "github.com/G-Node/gin-cli/ginclient"
	"github.com/G-Node/gin-cli/ginclient/config"
	"github.com/G-Node/gin-cli/gincmd/ginerrors"
	"github.com/G-Node/gin-cli/git"
	"github.com/spf13/cobra"
)

func countItemsUnlock(paths []string) (count int) {
	wichan := make(chan git.AnnexWhereisRes)
	go git.AnnexWhereis(paths, wichan)
	for range wichan {
		count++
	}
	return
}

func unlock(cmd *cobra.Command, args []string) {
	jsonout, _ := cmd.Flags().GetBool("json")
	if !git.IsRepo() {
		Die(ginerrors.NotInRepo)
	}
	// unlock should do nothing in direct mode
	if git.IsDirect() {
		return
	}

	// TODO: Probably doesn't need a server config
	conf := config.Read()
	defserver := conf.DefaultServer
	gincl := ginclient.New(defserver)
	nitems := countItemsUnlock(args)
	unlockchan := make(chan git.RepoFileStatus)
	go gincl.UnlockContent(args, unlockchan)
	formatOutput(unlockchan, nitems, jsonout)
}

// UnlockCmd sets up the file 'unlock' subcommand
func UnlockCmd() *cobra.Command {
	description := "Unlock one or more files for editing. Files added to the repository are left in a locked state, which allows reading but prevents editing. In order to edit or write to a file, it must first be unlocked. When done editing, it is recommended that the file be locked again using the 'lock' command.\n\nAfter performing an 'upload, 'download', or 'get', affected files are always reverted to the locked state.\n\nUnlocking a file takes longer depending on its size."
	args := map[string]string{
		"<filenames>": "One or more directories or files to unllock.",
	}
	var cmd = &cobra.Command{
		Use:                   "unlock [--json] [<filenames>]...",
		Short:                 "Unlock files for editing",
		Long:                  formatdesc(description, args),
		Args:                  cobra.ArbitraryArgs,
		Run:                   unlock,
		DisableFlagsInUseLine: true,
	}
	cmd.Flags().Bool("json", false, "Print output in JSON format.")
	return cmd
}
