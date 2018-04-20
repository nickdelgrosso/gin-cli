package gincmd

import (
	"fmt"

	ginclient "github.com/G-Node/gin-cli/ginclient"
	"github.com/G-Node/gin-cli/ginclient/config"
	"github.com/G-Node/gin-cli/git"
	"github.com/fatih/color"
	"github.com/spf13/cobra"
)

func download(cmd *cobra.Command, args []string) {
	jsonout, _ := cmd.Flags().GetBool("json")
	gincl := ginclient.New(config.Config.GinHost)
	requirelogin(cmd, gincl, !jsonout)
	if !git.IsRepo() {
		Die("This command must be run from inside a gin repository.")
	}

	content, _ := cmd.Flags().GetBool("content")
	gincl.GitHost = config.Config.GitHost
	gincl.GitUser = config.Config.GitUser
	lockchan := make(chan git.RepoFileStatus)
	go gincl.LockContent([]string{}, lockchan)
	formatOutput(lockchan, jsonout)
	if !jsonout {
		fmt.Print("Downloading changes ")
	}
	err := gincl.Download()
	CheckError(err)
	if !jsonout {
		fmt.Fprintln(color.Output, green("OK"))
	}
	if content {
		reporoot, _ := git.FindRepoRoot(".")
		ginclient.Workingdir = reporoot
		getContent(cmd, nil)
	}
}

// DownloadCmd sets up the 'download' subcommand
func DownloadCmd() *cobra.Command {
	description := "Downloads changes from the remote repository to the local clone. This will create new files that were added remotely, delete files that were removed, and update files that were changed.\n\nOptionally downloads the content of all files in the repository. If 'content' is not specified, new files will be empty placeholders. Content of individual files can later be retrieved using the 'get-content' command."
	var downloadCmd = &cobra.Command{
		Use:   "download [--json] [--content]",
		Short: "Download all new information from a remote repository",
		Long:  formatdesc(description, nil),
		Args:  cobra.NoArgs,
		Run:   download,
		DisableFlagsInUseLine: true,
	}
	downloadCmd.Flags().Bool("json", false, "Print output in JSON format.")
	downloadCmd.Flags().Bool("content", false, "Download the content for all files in the repository.")
	return downloadCmd
}
