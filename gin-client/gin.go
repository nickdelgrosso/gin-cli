package ginclient

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"

	"net/http"

	"github.com/G-Node/gin-cli/util"
	"github.com/G-Node/gin-cli/web"
	gogs "github.com/gogits/go-gogs-client"
)

// ginerror convenience alias to util.Error
type ginerror = util.Error

// GINUser represents a API user.
type GINUser struct {
	ID        int64  `json:"id"`
	UserName  string `json:"login"`
	FullName  string `json:"full_name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

// Client is a client interface to the GIN server. Embeds web.Client.
type Client struct {
	*web.Client
	GitHost string
	GitUser string
}

// NewClient returns a new client for the GIN server.
func NewClient(host string) *Client {
	return &Client{Client: web.NewClient(host)}
}

// GetUserKeys fetches the public keys that the user has added to the auth server.
func (gincl *Client) GetUserKeys() ([]gogs.PublicKey, error) {
	fn := "GetUserKeys()"
	var keys []gogs.PublicKey
	res, err := gincl.Get("/api/v1/user/keys")
	if err != nil {
		return nil, err // return error from Get() directly
	}
	switch code := res.StatusCode; {
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
	err = json.Unmarshal(b, &keys)
	if err != nil {
		return nil, ginerror{UError: err.Error(), Origin: fn, Description: "failed to parse response body"}
	}
	return keys, nil
}

// RequestAccount requests a specific account by name.
func (gincl *Client) RequestAccount(name string) (gogs.User, error) {
	fn := fmt.Sprintf("RequestAccount(%s)", name)
	var acc gogs.User
	res, err := gincl.Get(fmt.Sprintf("/api/v1/users/%s", name))
	if err != nil {
		return acc, err // return error from Get() directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusNotFound:
		return acc, ginerror{UError: res.Status, Origin: fn, Description: fmt.Sprintf("requested user '%s' does not exist", name)}
	case code == http.StatusUnauthorized:
		return acc, ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code == http.StatusInternalServerError:
		return acc, ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code != http.StatusOK:
		return acc, ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}

	defer web.CloseRes(res.Body)

	b, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return acc, ginerror{UError: err.Error(), Origin: fn, Description: "failed to read response body"}
	}
	err = json.Unmarshal(b, &acc)
	if err != nil {
		err = ginerror{UError: err.Error(), Origin: fn, Description: "failed to parse response body"}
	}
	return acc, err
}

// AddKey adds the given key to the current user's authorised keys.
// If force is enabled, any key which matches the new key's description will be overwritten.
func (gincl *Client) AddKey(key, description string, force bool) error {
	fn := "AddKey()"
	newkey := gogs.PublicKey{Key: key, Title: description}

	if force {
		// Attempting to delete potential existing key that matches the title
		_ = gincl.DeletePubKey(description)
	}

	address := fmt.Sprintf("/api/v1/user/keys")
	res, err := gincl.Post(address, newkey)
	if err != nil {
		return err // return error from Post() directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusUnprocessableEntity:
		return ginerror{UError: res.Status, Origin: fn, Description: "invalid key or key with same name already exists"}
	case code == http.StatusUnauthorized:
		return ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code == http.StatusInternalServerError:
		return ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code != http.StatusCreated:
		return ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}
	web.CloseRes(res.Body)
	return nil
}

// DeletePubKey removes the key that matches the given description (title) from the current user's authorised keys.
func (gincl *Client) DeletePubKey(description string) error {
	fn := "DeletePubKey()"
	keys, err := gincl.GetUserKeys()
	if err != nil {
		util.LogWrite("Error when getting user keys: %v", err)
		return err
	}
	var id int64
	for _, key := range keys {
		if key.Title == description {
			id = key.ID
			break
		}
	}

	address := fmt.Sprintf("/api/v1/user/keys/%d", id)
	res, err := gincl.Delete(address)
	defer web.CloseRes(res.Body)
	if err != nil {
		return err // Return error from Delete() directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusInternalServerError:
		return ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code == http.StatusUnauthorized:
		return ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code == http.StatusForbidden:
		return ginerror{UError: res.Status, Origin: fn, Description: "failed to delete key (forbidden)"}
	case code != http.StatusNoContent:
		return ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}
	return nil
}

// Login requests a token from the auth server and stores the username and token to file.
// It also generates a key pair for the user for use in git commands.
func (gincl *Client) Login(username, password, clientID string) error {
	fn := "Login()"
	tokenCreate := &gogs.CreateAccessTokenOption{Name: "gin-cli"}
	address := fmt.Sprintf("/api/v1/users/%s/tokens", username)
	res, err := gincl.PostBasicAuth(address, username, password, tokenCreate)
	if err != nil {
		return err // return error from PostBasicAuth directly
	}
	switch code := res.StatusCode; {
	case code == http.StatusInternalServerError:
		return ginerror{UError: res.Status, Origin: fn, Description: "server error"}
	case code == http.StatusUnauthorized:
		return ginerror{UError: res.Status, Origin: fn, Description: "authorisation failed"}
	case code != http.StatusCreated:
		return ginerror{UError: res.Status, Origin: fn} // Unexpected error
	}
	data, err := ioutil.ReadAll(res.Body)
	if err != nil {
		return err
	}
	util.LogWrite("Got resonse: %s", res.Status)
	token := AccessToken{}
	err = json.Unmarshal(data, &token)
	if err != nil {
		return ginerror{UError: err.Error(), Origin: fn, Description: "failed to parse response body"}
	}
	gincl.Username = username
	gincl.Token = token.Sha1
	util.LogWrite("Login successful. Username: %s", username)

	err = gincl.StoreToken()
	if err != nil {
		return fmt.Errorf("Error while storing token: %s", err.Error())
	}

	return gincl.MakeSessionKey()
}

// Logout logs out the currently logged in user in 3 steps:
// 1. Remove the public key matching the current hostname from the server.
// 2. Delete the private key file from the local machine.
// 3. Delete the user token.
func (gincl *Client) Logout() {
	// 1. Delete public key
	hostname, err := os.Hostname()
	if err != nil {
		util.LogWrite("Could not retrieve hostname")
		hostname = defaultHostname
	}

	currentkeyname := fmt.Sprintf("%s@%s", gincl.Username, hostname)
	_ = gincl.DeletePubKey(currentkeyname)

	// 2. Delete private key
	privKeyFile := util.PrivKeyPath(gincl.UserToken.Username)
	err = os.Remove(privKeyFile)
	if err != nil {
		util.LogWrite("Error deleting key file")
	} else {
		util.LogWrite("Private key file deleted")
	}

	err = web.DeleteToken()
	if err != nil {
		util.LogWrite("Error deleting token file")
	}
}

// AccessToken represents a API access token.
type AccessToken struct {
	Name string `json:"name"`
	Sha1 string `json:"sha1"`
}
