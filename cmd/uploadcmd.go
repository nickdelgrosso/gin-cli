package gincmd

import (
	"fmt"
	"os"

	ginclient "github.com/G-Node/gin-cli/gin-client"
	"github.com/G-Node/gin-cli/util"
	"github.com/spf13/cobra"
)

func upload(cmd *cobra.Command, args []string) {
	jsonout, _ := cmd.Flags().GetBool("json")
	gincl := ginclient.NewClient(util.Config.GinHost)
	requirelogin(cmd, gincl, !jsonout)
	if !ginclient.IsRepo() {
		util.Die("This command must be run from inside a gin repository.")
	}

	gincl.GitHost = util.Config.GitHost
	gincl.GitUser = util.Config.GitUser

	lockchan := make(chan ginclient.RepoFileStatus)
	go gincl.LockContent(args, lockchan)
	printProgress(lockchan, jsonout)

	// add header commit line
	hostname, err := os.Hostname()
	if err != nil {
		util.LogWrite("Could not retrieve hostname")
		hostname = "(unknown)"
	}
	commitmsg := fmt.Sprintf("gin upload from %s\n\n%s", hostname, getchanges())
	uploadchan := make(chan ginclient.RepoFileStatus)
	go gincl.Upload(args, commitmsg, uploadchan)
	printProgress(uploadchan, jsonout)
}

func getchanges() string {
	changes, err := ginclient.DescribeIndexShort()
	if err != nil {
		util.LogWrite("Failed to determine file changes for commit message")
	}
	if changes == "" {
		changes = "No changes recorded"
	}
	return changes
}

// UploadCmd sets up the 'upload' subcommand
func UploadCmd() *cobra.Command {
	description := "Upload changes made in a local repository clone to the remote repository on the GIN server. This command must be called from within the local repository clone. Specific files or directories may be specified. All changes made will be sent to the server, including addition of new files, modifications and renaming of existing files, and file deletions.\n\nIf no arguments are specified, only changes to files already being tracked are uploaded."
	args := map[string]string{"<filenames>": "One or more directories or files to upload and update."}
	var uploadCmd = &cobra.Command{
		Use:   "upload [--json] [<filenames>]...",
		Short: "Upload local changes to a remote repository",
		Long:  formatdesc(description, args),
		Args:  cobra.ArbitraryArgs,
		Run:   upload,
		DisableFlagsInUseLine: true,
	}
	uploadCmd.Flags().Bool("json", false, "Print output in JSON format.")
	return uploadCmd
}