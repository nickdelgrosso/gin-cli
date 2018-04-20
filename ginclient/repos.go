package ginclient

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/G-Node/gin-cli/ginclient/log"
	"github.com/G-Node/gin-cli/git"
	"github.com/G-Node/gin-cli/util"
	"github.com/G-Node/gin-cli/web"
	"github.com/gogits/go-gogs-client"
)

// High level functions for managing repositories.
// These functions either end up performing web calls (using the web package) or git shell commands (using the git package).

const unknownhostname = "(unknown)"

// Workingdir sets the directory for shell commands
var Workingdir = "."

// Types

// FileCheckoutStatus is used to report the status of a CheckoutFileCopies() operation.
type FileCheckoutStatus struct {
	Filename    string
	Type        string
	Destination string
	Err         error
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

//isAnnexPath returns true if a given string represents the path to an annex object.
func isAnnexPath(path string) bool {
	// TODO: Check paths on Windows
	return strings.Contains(path, ".git/annex/objects")
}

// MakeSessionKey creates a private+public key pair.
// The private key is saved in the user's configuration directory, to be used for git commands.
// The public key is added to the GIN server for the current logged in user.
func (gincl *Client) MakeSessionKey() error {
	keyPair, err := git.MakeKeyPair()
	if err != nil {
		return err
	}

	hostname, err := os.Hostname()
	if err != nil {
		log.LogWrite("Could not retrieve hostname")
		hostname = unknownhostname
	}
	description := fmt.Sprintf("GIN Client: %s@%s", gincl.Username, hostname)
	pubkey := fmt.Sprintf("%s %s", strings.TrimSpace(keyPair.Public), description)
	err = gincl.AddKey(pubkey, description, true)
	if err != nil {
		return err
	}

	privKeyFile := git.PrivKeyPath(gincl.Username)
	_ = ioutil.WriteFile(privKeyFile, []byte(keyPair.Private), 0600)

	return nil
}

// GetRepo retrieves the information of a repository.
func (gincl *Client) GetRepo(repoPath string) (gogs.Repository, error) {
	fn := fmt.Sprintf("GetRepo(%s)", repoPath)
	log.LogWrite("GetRepo")
	var repo gogs.Repository

	res, err := gincl.Get(fmt.Sprintf("/api/v1/repos/%s", repoPath))
	if err != nil {
		return repo, err // return error from Get() directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusNotFound:
		return repo, ginerror{UError: res.Status, Origin: fn, Description: fmt.Sprintf("repository '%s' does not exist", repoPath)}
	case code == http.StatusUnauthorized:
		return repo, ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code == http.StatusInternalServerError:
		return repo, ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code != http.StatusOK:
		return repo, ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}
	defer web.CloseRes(res.Body)
	b, err := ioutil.ReadAll(res.Body) // ignore potential read error on res.Body; catch later when trying to unmarshal
	if err != nil {
		return repo, ginerror{UError: err.Error(), Origin: fn, Description: "failed to read response body"}
	}
	err = json.Unmarshal(b, &repo)
	if err != nil {
		return repo, ginerror{UError: err.Error(), Origin: fn, Description: "failed to parse response body"}
	}
	return repo, nil
}

// ListRepos gets a list of repositories (public or user specific)
func (gincl *Client) ListRepos(user string) ([]gogs.Repository, error) {
	fn := fmt.Sprintf("ListRepos(%s)", user)
	log.LogWrite("Retrieving repo list")
	var repoList []gogs.Repository
	var res *http.Response
	var err error
	res, err = gincl.Get(fmt.Sprintf("/api/v1/users/%s/repos", user))
	if err != nil {
		return nil, err // return error from Get() directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusNotFound:
		return nil, ginerror{UError: res.Status, Origin: fn, Description: fmt.Sprintf("user '%s' does not exist", user)}
	case code == http.StatusUnauthorized:
		return nil, ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code == http.StatusInternalServerError:
		return nil, ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code != http.StatusOK:
		return nil, ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}
	defer web.CloseRes(res.Body)
	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return nil, ginerror{UError: err.Error(), Origin: fn, Description: "failed to read response body"}
	}
	err = json.Unmarshal(b, &repoList)
	if err != nil {
		return nil, ginerror{UError: err.Error(), Origin: fn, Description: "failed to parse response body"}
	}
	return repoList, nil
}

// CreateRepo creates a repository on the server.
func (gincl *Client) CreateRepo(name, description string) error {
	fn := fmt.Sprintf("CreateRepo(name)")
	log.LogWrite("Creating repository")
	newrepo := gogs.CreateRepoOption{Name: name, Description: description, Private: true}
	log.LogWrite("Name: %s :: Description: %s", name, description)
	res, err := gincl.Post("/api/v1/user/repos", newrepo)
	if err != nil {
		return err // return error from Post() directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusUnprocessableEntity:
		return ginerror{UError: res.Status, Origin: fn, Description: "invalid repository name or repository with the same name already exists"}
	case code == http.StatusUnauthorized:
		return ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code == http.StatusInternalServerError:
		return ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code != http.StatusCreated:
		return ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}
	web.CloseRes(res.Body)
	log.LogWrite("Repository created")
	return nil
}

// DelRepo deletes a repository from the server.
func (gincl *Client) DelRepo(name string) error {
	fn := fmt.Sprintf("DelRepo(%s)", name)
	log.LogWrite("Deleting repository")
	res, err := gincl.Delete(fmt.Sprintf("/api/v1/repos/%s", name))
	if err != nil {
		return err // return error from Post() directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusForbidden:
		return ginerror{UError: res.Status, Origin: fn, Description: "failed to delete repository (forbidden)"}
	case code == http.StatusNotFound:
		return ginerror{UError: res.Status, Origin: fn, Description: fmt.Sprintf("repository '%s' does not exist", name)}
	case code == http.StatusUnauthorized:
		return ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code == http.StatusInternalServerError:
		return ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code != http.StatusNoContent:
		return ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}
	web.CloseRes(res.Body)
	log.LogWrite("Repository deleted")
	return nil
}

// Add updates the index with the changes in the files specified by 'paths'.
// The status channel 'addchan' is closed when this function returns.
func Add(paths []string, addchan chan<- git.RepoFileStatus) {
	defer close(addchan)
	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		addchan <- git.RepoFileStatus{Err: err}
		return
	}

	if len(paths) > 0 {
		// Run git annex add using exclusion filters and then add the rest to git
		annexaddchan := make(chan git.RepoFileStatus)
		go git.AnnexAdd(paths, annexaddchan)
		for addstat := range annexaddchan {
			addchan <- addstat
		}

		gitaddchan := make(chan git.RepoFileStatus)
		go git.Add(paths, gitaddchan)
		for addstat := range gitaddchan {
			addchan <- addstat
		}
	}
}

// Upload transfers locally recorded changes to a remote.
// The status channel 'uploadchan' is closed when this function returns.
func (gincl *Client) Upload(paths []string, uploadchan chan<- git.RepoFileStatus) {
	defer close(uploadchan)
	log.LogWrite("Upload")

	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		uploadchan <- git.RepoFileStatus{Err: err}
		return
	}

	annexpushchan := make(chan git.RepoFileStatus)
	go git.AnnexPush(paths, annexpushchan)
	for stat := range annexpushchan {
		uploadchan <- stat
	}
	return
}

// GetContent downloads the contents of placeholder files in a checked out repository.
// The status channel 'getcontchan' is closed when this function returns.
func (gincl *Client) GetContent(paths []string, getcontchan chan<- git.RepoFileStatus) {
	defer close(getcontchan)
	log.LogWrite("GetContent")

	paths, err := util.ExpandGlobs(paths)

	if err != nil {
		getcontchan <- git.RepoFileStatus{Err: err}
		return
	}

	annexgetchan := make(chan git.RepoFileStatus)
	go git.AnnexGet(paths, annexgetchan)
	for stat := range annexgetchan {
		getcontchan <- stat
	}
	return
}

// RemoveContent removes the contents of local files, turning them into placeholders but only if the content is available on a remote.
// The status channel 'rmcchan' is closed when this function returns.
func (gincl *Client) RemoveContent(paths []string, rmcchan chan<- git.RepoFileStatus) {
	defer close(rmcchan)
	log.LogWrite("RemoveContent")

	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		rmcchan <- git.RepoFileStatus{Err: err}
		return
	}

	dropchan := make(chan git.RepoFileStatus)
	go git.AnnexDrop(paths, dropchan)
	for stat := range dropchan {
		rmcchan <- stat
	}
	return
}

// LockContent locks local files, turning them into symlinks (if supported by the filesystem).
// The status channel 'lockchan' is closed when this function returns.
func (gincl *Client) LockContent(paths []string, lcchan chan<- git.RepoFileStatus) {
	defer close(lcchan)
	log.LogWrite("LockContent")

	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		lcchan <- git.RepoFileStatus{Err: err}
		return
	}

	lockchan := make(chan git.RepoFileStatus)
	go git.AnnexLock(paths, lockchan)
	for stat := range lockchan {
		lcchan <- stat
	}
	return
}

// UnlockContent unlocks local files turning them into normal files, if the content is locally available.
// The status channel 'unlockchan' is closed when this function returns.
func (gincl *Client) UnlockContent(paths []string, ulcchan chan<- git.RepoFileStatus) {
	defer close(ulcchan)
	log.LogWrite("UnlockContent")

	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		ulcchan <- git.RepoFileStatus{Err: err}
		return
	}

	unlockchan := make(chan git.RepoFileStatus)
	go git.AnnexUnlock(paths, unlockchan)
	for stat := range unlockchan {
		ulcchan <- stat
	}
	return
}

// Download downloads changes and placeholder files in an already checked out repository.
// Setting the Workingdir package global affects the working directory in which the command is executed.
func (gincl *Client) Download() error {
	log.LogWrite("Download")
	return git.AnnexPull()
}

// CloneRepo clones a remote repository and initialises annex.
// The status channel 'clonechan' is closed when this function returns.
func (gincl *Client) CloneRepo(repoPath string, clonechan chan<- git.RepoFileStatus) {
	defer close(clonechan)
	log.LogWrite("CloneRepo")
	clonestatus := make(chan git.RepoFileStatus)
	remotepath := fmt.Sprintf("ssh://%s@%s/%s", gincl.GitUser, gincl.GitHost, repoPath)
	go git.Clone(remotepath, repoPath, clonestatus)
	for stat := range clonestatus {
		clonechan <- stat
		if stat.Err != nil {
			return
		}
	}

	repoPathParts := strings.SplitN(repoPath, "/", 2)
	repoName := repoPathParts[1]
	git.Workingdir = repoName

	status := git.RepoFileStatus{State: "Initialising local storage"}
	clonechan <- status
	err := gincl.InitDir()
	if err != nil {
		status.Err = err
		clonechan <- status
		return
	}
	status.Progress = "100%"
	clonechan <- status
	return
}

// CheckoutVersion checks out all files specified by paths from the revision with the specified commithash.
func CheckoutVersion(commithash string, paths []string) error {
	return git.Checkout(commithash, paths)
}

// CheckoutFileCopies checks out copies of files specified by path from the revision with the specified commithash.
// The checked out files are stored in the location specified by outpath.
// The timestamp of the revision is appended to the original filenames.
func CheckoutFileCopies(commithash string, paths []string, outpath string, suffix string, cochan chan<- FileCheckoutStatus) {
	defer close(cochan)
	objects, err := git.LsTree(commithash, paths)
	if err != nil {
		cochan <- FileCheckoutStatus{Err: err}
		return
	}

	for _, obj := range objects {
		if obj.Type == "blob" {
			var status FileCheckoutStatus
			status.Filename = obj.Name

			filext := filepath.Ext(obj.Name)
			outfilename := fmt.Sprintf("%s-%s%s", strings.TrimSuffix(obj.Name, filext), suffix, filext)
			outfile := filepath.Join(outpath, outfilename)
			status.Destination = outfile

			// determine if it's an annexed link
			content, cerr := git.CatFileContents(commithash, obj.Name)
			if cerr != nil {
				cochan <- FileCheckoutStatus{Err: cerr}
				return
			}
			if mderr := os.MkdirAll(outpath, 0777); mderr != nil {
				cochan <- FileCheckoutStatus{Err: mderr}
				return
			}
			if obj.Mode == "120000" {
				linkdst := string(content)
				if isAnnexPath(linkdst) {
					status.Type = "Annex"
					_, key := path.Split(linkdst)
					fkerr := git.AnnexFromKey(key, outfile)
					if fkerr != nil {
						status.Err = fmt.Errorf("Error creating placeholder file %s: %s", outfile, fkerr.Error())
					}
				} else {
					status.Type = "Link"
					status.Destination = string(content)
				}
			} else if obj.Mode == "100755" || obj.Mode == "100644" {
				status.Type = "Git"
				werr := ioutil.WriteFile(outfile, content, 0666)
				if werr != nil {
					status.Err = fmt.Errorf("Error writing %s: %s", outfile, werr.Error())
				}
			}
			cochan <- status
		}
	}
}

// AddRemote constructs the proper remote URL given a repository path (user/reponame) and adds it as a named remote to the repository configuration.
func (gincl *Client) AddRemote(name, repopath string) error {
	remotepath := fmt.Sprintf("ssh://%s@%s/%s", gincl.GitUser, gincl.GitHost, repopath)
	return git.AddRemote(name, remotepath)
}

// InitDir initialises the local directory with the default remote and annex configuration.
// The status channel 'initchan' is closed when this function returns.
func (gincl *Client) InitDir() error {
	initerr := ginerror{Origin: "InitDir", Description: "Error initialising local directory"}
	if !git.IsRepo() {
		cmd := git.Command("init")
		stdout, stderr, err := cmd.OutputError()
		if err != nil {
			log.LogWrite("Error during Init command: %s", string(stderr))
			log.LogWrite("[stdout]\n%s\n[stderr]\n%s", string(stdout), string(stderr))
			initerr.UError = err.Error()
			return initerr
		}
		Workingdir = "."
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = unknownhostname
	}
	description := fmt.Sprintf("%s@%s", gincl.Username, hostname)

	// If there is no global git user.name or user.email set local ones
	cmd := git.Command("config", "--global", "user.name")
	globalGitName, _ := cmd.Output()
	cmd = git.Command("config", "--global", "user.email")
	globalGitEmail, _ := cmd.Output()
	if len(globalGitName) == 0 && len(globalGitEmail) == 0 {
		info, ierr := gincl.RequestAccount(gincl.Username)
		name := info.FullName
		if ierr != nil || name == "" {
			name = gincl.Username
		}
		ierr = git.SetGitUser(name, info.Email)
		if ierr != nil {
			log.LogWrite("Failed to set local git user configuration")
		}
	}
	if runtime.GOOS == "windows" {
		// force disable symlinks even if user can create them
		// see https://git-annex.branchable.com/bugs/Symlink_support_on_Windows_10_Creators_Update_with_Developer_Mode/
		git.Command("config", "--local", "core.symlinks", "false").Run()
	}

	err = git.AnnexInit(description)
	if err != nil {
		initerr.UError = err.Error()
		return initerr
	}

	return nil
}

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

func lfDirect(paths ...string) (map[string]FileStatus, error) {
	statuses := make(map[string]FileStatus)

	wichan := make(chan git.AnnexWhereisRes)
	go git.AnnexWhereis(paths, wichan)
	for wiInfo := range wichan {
		if wiInfo.Err != nil {
			continue
		}
		fname := wiInfo.File
		for _, remote := range wiInfo.Whereis {
			// if no remotes are "here", the file is NoContent
			statuses[fname] = NoContent
			if remote.Here {
				if len(wiInfo.Whereis) > 1 {
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

	statuschan := make(chan git.AnnexStatusRes)
	go git.AnnexStatus(asargs, statuschan)
	for item := range statuschan {
		if item.Err != nil {
			return nil, item.Err
		}
		if item.Status == "?" {
			statuses[item.File] = Untracked
		} else if item.Status == "M" {
			statuses[item.File] = Modified
		} else if item.Status == "D" {
			statuses[item.File] = Removed
		}
	}

	// Unmodified files that are checked into git (not annex) do not show up
	// Need to run git ls-files and add only files that haven't been added yet
	lschan := make(chan string)
	go git.LsFiles(paths, lschan)
	for fname := range lschan {
		if _, ok := statuses[fname]; !ok {
			statuses[fname] = Synced
		}
	}

	return statuses, nil
}

func lfIndirect(paths ...string) (map[string]FileStatus, error) {
	// TODO: Determine if added files (LocalChanges) are new or not (new status needed?)
	statuses := make(map[string]FileStatus)

	cachedchan := make(chan string)
	var cachedfiles, modifiedfiles, untrackedfiles, deletedfiles []string
	// Collect checked in files
	lsfilesargs := append([]string{"--cached"}, paths...)
	go git.LsFiles(lsfilesargs, cachedchan)

	// Collect modified files
	modifiedchan := make(chan string)
	lsfilesargs = append([]string{"--modified"}, paths...)
	go git.LsFiles(lsfilesargs, modifiedchan)

	// Collect untracked files
	otherschan := make(chan string)
	lsfilesargs = append([]string{"--others"}, paths...)
	go git.LsFiles(lsfilesargs, otherschan)

	// Collect deleted files
	deletedchan := make(chan string)
	lsfilesargs = append([]string{"--deleted"}, paths...)
	go git.LsFiles(lsfilesargs, deletedchan)

	for {
		select {
		case fname, ok := <-cachedchan:
			if ok {
				cachedfiles = append(cachedfiles, fname)
			} else {
				cachedchan = nil
			}
		case fname, ok := <-modifiedchan:
			if ok {
				modifiedfiles = append(modifiedfiles, fname)
			} else {
				modifiedchan = nil
			}
		case fname, ok := <-otherschan:
			if ok {
				untrackedfiles = append(untrackedfiles, fname)
			} else {
				otherschan = nil
			}
		case fname, ok := <-deletedchan:
			if ok {
				deletedfiles = append(deletedfiles, fname)
			} else {
				deletedchan = nil
			}
		}
		if cachedchan == nil && modifiedchan == nil && otherschan == nil && deletedchan == nil {
			break
		}
	}

	// Run whereis on cached files
	wichan := make(chan git.AnnexWhereisRes)
	go git.AnnexWhereis(cachedfiles, wichan)
	for wiInfo := range wichan {
		if wiInfo.Err != nil {
			continue
		}
		fname := wiInfo.File
		for _, remote := range wiInfo.Whereis {
			// if no remotes are "here", the file is NoContent
			statuses[fname] = NoContent
			if remote.Here {
				if len(wiInfo.Whereis) > 1 {
					statuses[fname] = Synced
				} else {
					statuses[fname] = LocalChanges
				}
				break
			}
		}
	}

	// If cached files are diff from upstream, mark as LocalChanges
	diffargs := []string{"diff", "-z", "--name-only", "--relative", "@{upstream}"}
	diffargs = append(diffargs, cachedfiles...)
	cmd := git.Command(diffargs...)
	stdout, stderr, err := cmd.OutputError()
	if err != nil {
		log.LogWrite("Error during diff command for status")
		log.LogWrite("[stdout]\n%s\n[stderr]\n%s", string(stdout), string(stderr))
		// ignoring error and continuing
	}

	diffresults := strings.Split(string(stdout), "\000")
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
		statuschan := make(chan git.AnnexStatusRes)
		go git.AnnexStatus(modifiedfiles, statuschan)
		for item := range statuschan {
			if item.Err != nil {
				log.LogWrite("Error during annex status while searching for unlocked files")
				// lockchan <- git.RepoFileStatus{Err: item.Err}
			}
			if item.Status == "T" {
				statuses[item.File] = Unlocked
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
	paths, err := util.ExpandGlobs(paths)
	if err != nil {
		return nil, err
	}
	if git.IsDirect() {
		return lfDirect(paths...)
	}
	return lfIndirect(paths...)
}
