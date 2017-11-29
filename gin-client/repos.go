package ginclient

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"

	"github.com/G-Node/gin-cli/util"
	"github.com/G-Node/gin-cli/web"
	"github.com/fatih/color"
	"github.com/gogits/go-gogs-client"
	// its a bit unfortunate that we have that import now
	// but its only temporary...
)

var green = color.New(color.FgGreen)
var defaultHostname = "(unknown)"

// MakeSessionKey creates a private+public key pair.
// The private key is saved in the user's configuration directory, to be used for git commands.
// The public key is added to the GIN server for the current logged in user.
func (gincl *Client) MakeSessionKey() error {
	keyPair, err := util.MakeKeyPair()
	if err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		util.LogWrite("Could not retrieve hostname")
		hostname = defaultHostname
	}
	description := fmt.Sprintf("GIN Client: %s@%s", gincl.Username, hostname)
	pubkey := fmt.Sprintf("%s %s", strings.TrimSpace(keyPair.Public), description)
	err = gincl.AddKey(pubkey, description, true)
	if err != nil {
		return err
	}

	privKeyFile := util.PrivKeyPath(gincl.Username)
	_ = ioutil.WriteFile(privKeyFile, []byte(keyPair.Private), 0600)

	return nil
}

// GetRepo retrieves the information of a repository.
func (gincl *Client) GetRepo(repoPath string) (gogs.Repository, error) {
	util.LogWrite("GetRepo")
	var repo gogs.Repository

	res, err := gincl.Get(fmt.Sprintf("/api/v1/repos/%s", repoPath))
	if err != nil {
		return repo, err
	} else if res.StatusCode == http.StatusNotFound {
		return repo, fmt.Errorf("Not found. Check repository owner and name.")
	} else if res.StatusCode == http.StatusUnauthorized {
		return repo, fmt.Errorf("You are not authorised to access repository.")
	} else if res.StatusCode != http.StatusOK {
		return repo, fmt.Errorf("Server returned %s", res.Body)
	}
	defer web.CloseRes(res.Body)
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return repo, err
	}
	err = json.Unmarshal(b, &repo)
	return repo, err
}

// ListRepos gets a list of repositories (public or user specific)
func (gincl *Client) ListRepos(user string) ([]gogs.Repository, error) {
	util.LogWrite("Retrieving repo list")
	var repoList []gogs.Repository
	var res *http.Response
	var err error
	res, err = gincl.Get("/api/v1/user/repos")
	if err != nil {
		return repoList, err
	}
	defer web.CloseRes(res.Body)
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return repoList, err
	}
	err = json.Unmarshal(b, &repoList)
	return repoList, err
}

// CreateRepo creates a repository on the server.
func (gincl *Client) CreateRepo(name, description string) error {
	util.LogWrite("Creating repository")
	err := gincl.LoadToken()
	if err != nil {
		return fmt.Errorf("[Create repository] This action requires login")
	}

	newrepo := gogs.Repository{Name: name, Description: description, Private: true}
	util.LogWrite("Name: %s :: Description: %s", name, description)
	res, err := gincl.Post("/api/v1/user/repos", newrepo)
	if err != nil {
		return err
	} else if res.StatusCode != http.StatusCreated {
		return fmt.Errorf("[Create repository] Failed. Server returned %s", res.Status)
	}
	web.CloseRes(res.Body)
	util.LogWrite("Repository created")
	return nil
}

// DelRepo deletes a repository from the server.
func (gincl *Client) DelRepo(name string) error {
	util.LogWrite("Deleting repository")
	err := gincl.LoadToken()
	if err != nil {
		return fmt.Errorf("[Delete repository] This action requires login")
	}

	res, err := gincl.Delete(fmt.Sprintf("/api/v1/repos/%s", name))
	if err != nil {
		return err
	} else if res.StatusCode != http.StatusNoContent {
		return fmt.Errorf("[Delete repository] Failed. Server returned %s", res.Status)
	}
	web.CloseRes(res.Body)
	util.LogWrite("Repository deleted")
	return nil
}

// RepoFileStatus describes the status of files when being added to the repo or transfered to/from remotes.
type RepoFileStatus struct {
	// The name of the file.
	FileName string
	// The state of the operation (Added, Uploading, or Downloading).
	// TODO: Use enum
	State string
	// Progress of the operation, if available. This is empty when the State is "Added"
	Progress string
	// The data rate. This is empty when the State is "Added"
	Rate string
	// Errors
	Err error
}

// Upload adds files to a repository and uploads them.
// The status channel 'uploadchan' is closed when this function returns.
func (gincl *Client) Upload(paths []string, uploadchan chan<- RepoFileStatus) {
	defer close(uploadchan)
	util.LogWrite("Upload")

	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		uploadchan <- RepoFileStatus{Err: err}
		return
	}

	if len(paths) > 0 {
		// Run git annex add using exclusion filters and then add the rest to git
		addchan := make(chan RepoFileStatus)
		go AnnexAdd(paths, addchan)
		for {
			addstat, ok := <-addchan
			if !ok {
				break
			}
			// Send UploadStatus
			uploadchan <- addstat
		}

		addchan = make(chan RepoFileStatus)
		go GitAdd(paths, addchan)
		for {
			addstat, ok := <-addchan
			if !ok {
				break
			}
			// Send UploadStatus
			uploadchan <- addstat
		}

	}

	changes, err := DescribeIndexShort()
	if err != nil {
		uploadchan <- RepoFileStatus{Err: err}
		return
	}
	// add header commit line
	hostname, err := os.Hostname()
	if err != nil {
		util.LogWrite("Could not retrieve hostname")
		hostname = defaultHostname
	}
	if changes == "" {
		changes = "No changes recorded"
	}
	changes = fmt.Sprintf("gin upload from %s\n\n%s", hostname, changes)
	if err != nil {
		uploadchan <- RepoFileStatus{Err: err}
		return
	}

	annexpushchan := make(chan RepoFileStatus)
	go AnnexPush(paths, changes, annexpushchan)
	for {
		stat, ok := <-annexpushchan
		if !ok {
			break
		}
		uploadchan <- stat
	}
	return
}

// GetContent downloads the contents of placeholder files in a checked out repository.
// The status channel 'getcontchan' is closed when this function returns.
func (gincl *Client) GetContent(paths []string, getcontchan chan<- RepoFileStatus) {
	defer close(getcontchan)
	util.LogWrite("GetContent")

	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		getcontchan <- RepoFileStatus{Err: err}
		return
	}

	annexgetchan := make(chan RepoFileStatus)
	go AnnexGet(paths, annexgetchan)
	for {
		stat, ok := <-annexgetchan
		if !ok {
			break
		}
		getcontchan <- stat
	}
	return
}

// RemoveContent removes the contents of local files, turning them into placeholders but only if the content is available on a remote.
// The status channel 'rmcchan' is closed when this function returns.
func (gincl *Client) RemoveContent(paths []string, rmcchan chan<- RepoFileStatus) {
	defer close(rmcchan)
	util.LogWrite("RemoveContent")

	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		rmcchan <- RepoFileStatus{Err: err}
		return
	}

	dropchan := make(chan RepoFileStatus)
	go AnnexDrop(paths, dropchan)
	for {
		stat, ok := <-dropchan
		if !ok {
			break
		}
		rmcchan <- stat
	}
	return
}

// Download downloads changes and placeholder files in an already checked out repository.
// Setting the Workingdir package global affects the working directory in which the command is executed.
// The status channel 'downloadchan' is closed when this function returns.
func (gincl *Client) Download(content bool, downloadchan chan<- RepoFileStatus) {
	defer close(downloadchan)
	util.LogWrite("Download")

	downloadstatus := make(chan RepoFileStatus)
	go AnnexPull(content, downloadstatus)
	for {
		stat, ok := <-downloadstatus
		if !ok {
			break
		}
		downloadchan <- stat
	}
	return
}

// CloneRepo clones a remote repository and initialises annex.
// The status channel 'clonechan' is closed when this function returns.
func (gincl *Client) CloneRepo(repoPath string, clonechan chan<- RepoFileStatus) {
	defer close(clonechan)
	util.LogWrite("CloneRepo")
	err := gincl.LoadToken()
	if err != nil {
		clonechan <- RepoFileStatus{Err: err}
		return
	}

	clonestatus := make(chan RepoFileStatus)
	go gincl.Clone(repoPath, clonestatus)
	for {
		stat, ok := <-clonestatus
		if !ok {
			break
		}
		clonechan <- stat
	}
	_, repoName := splitRepoParts(repoPath)
	Workingdir = repoName

	initstatus := make(chan RepoFileStatus)
	go gincl.InitDir(repoPath, initstatus)
	for {
		stat, ok := <-initstatus
		if !ok {
			break
		}
		clonechan <- stat
	}
	return
}

// InitDir initialises the local directory with the default remote and annex configuration.
// The status channel 'initchan' is closed when this function returns.
func (gincl *Client) InitDir(repoPath string, initchan chan<- RepoFileStatus) {
	defer close(initchan)
	initerr := fmt.Errorf("Error initialising local directory")
	remotePath := fmt.Sprintf("ssh://%s@%s/%s", gincl.GitUser, gincl.GitHost, repoPath)

	var stat RepoFileStatus
	stat.State = "Initialising local storage"
	initchan <- stat
	if !IsRepo() {
		cmd, err := RunGitCommand("init")

		if err != nil || cmd.Wait() != nil {
			util.LogWrite("Error during Init command: %s", err.Error())
			cmd.LogStdOutErr()
			initchan <- RepoFileStatus{Err: initerr}
			return
		}
		Workingdir = "."
	}
	stat.Progress = "10%"
	initchan <- stat

	hostname, err := os.Hostname()
	if err != nil {
		hostname = defaultHostname
	}
	description := fmt.Sprintf("%s@%s", gincl.Username, hostname)

	// If there is no global git user.name or user.email set local ones
	cmd, _ := RunGitCommand("config", "--global", "user.name")
	globalGitName := cmd.OutPipe.ReadAll()
	cmd, _ = RunGitCommand("config", "--global", "user.Email")
	globalGitEmail := cmd.OutPipe.ReadAll()
	if len(globalGitName) == 0 && len(globalGitEmail) == 0 {
		info, ierr := gincl.RequestAccount(gincl.Username)
		name := info.FullName
		if ierr != nil {
			name = gincl.Username
		}
		ierr = SetGitUser(name, info.Email)
		if ierr != nil {
			util.LogWrite("Failed to set local git user configuration")
		}
	}
	stat.Progress = "20%"
	initchan <- stat

	// If there are no commits, create the initial commit.
	// While this isn't strictly necessary, it sets the active remote with commits that makes it easier to work with.
	new, err := CommitIfNew()
	if err != nil {
		initchan <- RepoFileStatus{Err: err}
		return
	}
	stat.Progress = "40%"
	initchan <- stat

	err = AnnexInit(description)
	if err != nil {
		initchan <- RepoFileStatus{Err: err}
		return
	}
	stat.Progress = "60%"
	initchan <- stat

	err = AddRemote("origin", remotePath)
	// Ignore if it already exists
	if err != nil && !strings.Contains(err.Error(), "already exists") {
		initchan <- RepoFileStatus{Err: err}
		return
	}
	stat.Progress = "80%"
	initchan <- stat

	if new {
		// Push initial commit and set default remote
		cmd, err := RunGitCommand("push", "--set-upstream", "origin", "master")
		if err != nil || cmd.Wait() != nil {
			util.LogWrite("Error during set upstream command")
			cmd.LogStdOutErr()
			initchan <- RepoFileStatus{Err: initerr}
			return
		}

		// Sync if an initial commit was created
		syncchan := make(chan RepoFileStatus)
		go AnnexSync(false, syncchan)
		<-syncchan // wait for channel to close
		if err != nil {
			initchan <- RepoFileStatus{Err: initerr}
			return
		}
	}
	stat.Progress = "100%"
	initchan <- stat
	return
}

// FileStatus represents the state a file is in with respect to local and remote changes.
type FileStatus uint8

const (
	// Synced indicates that an annexed file is synced between local and remote
	Synced FileStatus = iota
	// NoContent indicates that a file represents an annexed file that has not had its contents synced yet
	NoContent
	// Modified indicatres that a file has local modifications that have not been committed
	Modified
	// LocalChanges indicates that a file has local, committed modifications that have not been pushed
	LocalChanges
	// RemoteChanges indicates that a file has remote modifications that have not been pulled
	RemoteChanges
	// Unlocked indicates that a file is being tracked and is unlocked for editing
	Unlocked
	// Removed indicates that a (previously) tracked file has been deleted or moved
	Removed
	// Untracked indicates that a file is not being tracked by neither git nor git annex
	Untracked
)

// Description returns the long description of the file status
func (fs FileStatus) Description() string {
	switch {
	case fs == Synced:
		return "Synced"
	case fs == NoContent:
		return "No local content"
	case fs == Modified:
		return "Locally modified (unsaved)"
	case fs == LocalChanges:
		return "Locally modified (not uploaded)"
	case fs == RemoteChanges:
		return "Remotely modified (not downloaded)"
	case fs == Unlocked:
		return "Unlocked for editing"
	case fs == Removed:
		return "Removed"
	case fs == Untracked:
		return "Untracked"
	default:
		return "Unknown"
	}
}

// Abbrev returns the two-letter abbrevation of the file status
// OK (Synced), NC (NoContent), MD (Modified), LC (LocalUpdates), RC (RemoteUpdates), UL (Unlocked), RM (Removed), ?? (Untracked)
func (fs FileStatus) Abbrev() string {
	switch {
	case fs == Synced:
		return "OK"
	case fs == NoContent:
		return "NC"
	case fs == Modified:
		return "MD"
	case fs == LocalChanges:
		return "LC"
	case fs == RemoteChanges:
		return "RC"
	case fs == Unlocked:
		return "UL"
	case fs == Removed:
		return "RM"
	case fs == Untracked:
		return "??"
	default:
		return "??"
	}
}

// FileStatusSlice is a slice of FileStatus which implements Len() and Less() to allow sorting.
type FileStatusSlice []FileStatus

// Len is the number of elements in FileStatusSlice.
func (fsSlice FileStatusSlice) Len() int {
	return len(fsSlice)
}

// Swap swaps the elements with intexes i and j.
func (fsSlice FileStatusSlice) Swap(i, j int) {
	fsSlice[i], fsSlice[j] = fsSlice[j], fsSlice[i]
}

// Less reports whether the element with index i should sort before the element with index j.
func (fsSlice FileStatusSlice) Less(i, j int) bool {
	return fsSlice[i] < fsSlice[j]
}

func lfDirect(paths ...string) (map[string]FileStatus, error) {
	statuses := make(map[string]FileStatus)

	wiResults, _ := AnnexWhereis(paths)
	for _, status := range wiResults {
		fname := status.File
		for _, remote := range status.Whereis {
			// if no remotes are "here", the file is NoContent
			statuses[fname] = NoContent
			if remote.Here {
				if len(status.Whereis) > 1 {
					statuses[fname] = Synced
				} else {
					statuses[fname] = LocalChanges
				}
				break
			}
		}
	}

	asargs := paths
	if len(asargs) == 0 {
		// AnnexStatus with no arguments defaults to root directory, so we should use "." instead
		asargs = []string{"."}
	}
	annexstatuses, _ := AnnexStatus(asargs...)
	for _, stat := range annexstatuses {
		if stat.Status == "?" {
			statuses[stat.File] = Untracked
		} else if stat.Status == "M" {
			statuses[stat.File] = Modified
		} else if stat.Status == "D" {
			statuses[stat.File] = Removed
		}
	}

	// Unmodified files that are checked into git (not annex) do not show up
	// Need to unset 'bare' and run git ls-files and add only files that haven't been added yet
	filelist, _ := GitLsFiles(paths)
	for _, fname := range filelist {
		if _, ok := statuses[fname]; !ok {
			statuses[fname] = Synced
		}
	}

	return statuses, nil
}

func lfIndirect(paths ...string) (map[string]FileStatus, error) {
	statuses := make(map[string]FileStatus)

	// Collect checked in files
	lsfilesargs := append([]string{"--cached"}, paths...)
	cachedfiles, _ := GitLsFiles(lsfilesargs)

	// Collect modified files
	lsfilesargs = append([]string{"--modified"}, paths...)
	modifiedfiles, _ := GitLsFiles(lsfilesargs)

	// Collect untracked files
	lsfilesargs = append([]string{"--others"}, paths...)
	untrackedfiles, _ := GitLsFiles(lsfilesargs)

	// Collect deleted files
	lsfilesargs = append([]string{"--deleted"}, paths...)
	deletedfiles, _ := GitLsFiles(lsfilesargs)

	// Run whereis on cached files
	wiResults, err := AnnexWhereis(cachedfiles)
	if err == nil {
		for _, status := range wiResults {
			fname := status.File
			for _, remote := range status.Whereis {
				// if no remotes are "here", the file is NoContent
				statuses[fname] = NoContent
				if remote.Here {
					if len(status.Whereis) > 1 {
						statuses[fname] = Synced
					} else {
						statuses[fname] = LocalChanges
					}
					break
				}
			}
		}
	}

	// If cached files are diff from upstream, mark as LocalChanges
	diffargs := []string{"diff", "--name-only", "--relative", "@{upstream}"}
	diffargs = append(diffargs, cachedfiles...)
	cmd, err := RunGitCommand(diffargs...)
	if err != nil || cmd.Wait() != nil {
		util.LogWrite("Error during diff command for status")
		cmd.LogStdOutErr()
		// ignoring error and continuing
	}

	diffresults := strings.Split(cmd.OutPipe.ReadAll(), "\n")
	for _, fname := range diffresults {
		// Two notes:
		//		1. There will definitely be overlap here with the same status in annex (not a problem)
		//		2. The diff might be due to remote or local changes, but for now we're going to assume local
		if strings.TrimSpace(fname) != "" {
			statuses[fname] = LocalChanges
		}
	}

	// Add leftover cached files to the map
	for _, fname := range cachedfiles {
		if _, ok := statuses[fname]; !ok {
			statuses[fname] = Synced
		}
	}

	// Add modified and untracked files to the map
	for _, fname := range modifiedfiles {
		statuses[fname] = Modified
	}

	// Check if modified files are actually annex unlocked instead
	if len(modifiedfiles) > 0 {
		mdfilestatus, err := AnnexStatus(modifiedfiles...)
		if err != nil {
			util.LogWrite("Error during annex status while searching for unlocked files")
		}
		for _, stat := range mdfilestatus {
			if stat.Status == "T" {
				statuses[stat.File] = Unlocked
			}
		}
	}

	// Add untracked files to the map
	for _, fname := range untrackedfiles {
		statuses[fname] = Untracked
	}

	// Add deleted files to the map
	for _, fname := range deletedfiles {
		statuses[fname] = Removed
	}

	return statuses, nil
}

// ListFiles lists the files and directories specified by paths and their sync status.
// Setting the Workingdir package global affects the working directory in which the command is executed.
func (gincl *Client) ListFiles(paths ...string) (map[string]FileStatus, error) {
	if IsDirect() {
		return lfDirect(paths...)
	}
	return lfIndirect(paths...)
}
