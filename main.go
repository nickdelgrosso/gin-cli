package main

// TODO: Nicer error handling. Print useful, descriptive messages.

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"github.com/G-Node/gin-cli/auth"
	"github.com/G-Node/gin-cli/repo"
	"github.com/docopt/docopt-go"
)

func closeRes(b io.ReadCloser) {
	err := b.Close()
	if err != nil {
		fmt.Println("Error during cleanup:", err)
	}
}

// condAppend Conditionally append to a buffer
func condAppend(b *bytes.Buffer, str *string) {
	if str != nil && *str != "" {
		b.WriteString(*str + " ")
	}
}

func printKeys(printFull bool) error {

	keys, err := auth.GetUserKeys()
	if err != nil {
		return err
	}
	nkeys := len(keys)
	var plural string
	if nkeys == 1 {
		plural = ""
	} else {
		plural = "s"
	}
	fmt.Printf("You have %d key%s associated with your account.\n\n", nkeys, plural)
	for idx, key := range keys {
		fmt.Printf("  [%v] \"%s\"\n", idx+1, key.Description)
		fmt.Printf("  Fingerprint: %s\n", key.Fingerprint)
		if printFull {
			fmt.Printf("\n%s\n", key.Key)
		}
	}

	return err
}

func addKey() error {
	// TODO: Prompt user for key information
	// TODO: Allow use to speciry pubkey file (default to ~/.ssh/id_rsa.pub ?)
	key := ""

	err := auth.AddKey(key)
	return err
}

func printAccountInfo(userarg interface{}) error {
	var username string
	currentUser, token, _ := auth.LoadToken(true)

	if userarg == nil {
		username = currentUser
	} else {
		username = userarg.(string)
	}

	if username == "" {
		// prompt for username
		fmt.Print("Specify username for info lookup: ")
		username = ""
		fmt.Scanln(&username)
	}

	info, err := auth.RequestAccount(username, token)
	if err != nil {
		return err
	}

	var fullnameBuffer bytes.Buffer

	condAppend(&fullnameBuffer, info.Title)
	condAppend(&fullnameBuffer, &info.FirstName)
	condAppend(&fullnameBuffer, info.MiddleName)
	condAppend(&fullnameBuffer, &info.LastName)

	var outBuffer bytes.Buffer

	outBuffer.WriteString(fmt.Sprintf("User [%s]\nName: %s\n", info.Login, fullnameBuffer.String()))

	if info.Email != nil && info.Email.Email != "" {
		outBuffer.WriteString(fmt.Sprintf("Email: %s\n", info.Email.Email))
		// TODO: Display public status if current user == info.Login
	}

	if info.Affiliation != nil {
		var affiliationBuffer bytes.Buffer
		affiliation := info.Affiliation

		condAppend(&affiliationBuffer, &affiliation.Department)
		condAppend(&affiliationBuffer, &affiliation.Institute)
		condAppend(&affiliationBuffer, &affiliation.City)
		condAppend(&affiliationBuffer, &affiliation.Country)

		if affiliationBuffer.Len() > 0 {
			outBuffer.WriteString(fmt.Sprintf("Affiliation: %s\n", affiliationBuffer.String()))
		}
	}

	fmt.Println(outBuffer.String())

	return nil
}

func listUserRepos(userarg interface{}) error {
	var username string
	currentUser, token, _ := auth.LoadToken(true)

	if userarg == nil {
		username = currentUser
	} else {
		username = userarg.(string)
	}

	if username == "" {
		// prompt for username
		fmt.Print("Specify username for info lookup: ")
		username = ""
		fmt.Scanln(&username)
	}

	info, err := repo.GetRepos(username, token)
	if err != nil {
		return err
	}

	fmt.Printf("Repositories owned by %s\n", username)
	for idx, r := range info {
		print(idx)
		print("  ")
		println(r.Name)
	}

	return nil
}

func listPubRepos() error {
	repos, err := repo.GetRepos("", "")
	fmt.Printf("Public repositories\n")
	for idx, repoInfo := range repos {
		fmt.Printf("%d: %s [head: %s]\n", idx, repoInfo.Name, repoInfo.Head)
		fmt.Printf(" - %s\n", repoInfo.Description)
	}
	return err
}

func main() {
	usage := `
GIN command line client

Usage:
	gin login [<username>]
	gin repos [<username>]
	gin info  [<username>]
	gin keys  [-v | --verbose]
	gin keys add
	gin public
`

	args, _ := docopt.Parse(usage, nil, true, "gin cli 0.0", false)

	var err error

	switch {
	case args["login"].(bool):
		err = auth.Login(args["<username>"])
		if err != nil {
			fmt.Println("Authentication failed!")
		}
	case args["info"].(bool):
		err = printAccountInfo(args["<username>"])
	case args["keys"].(bool):
		if args["add"].(bool) {
			err = addKey()
		} else {
			printFullKeys := false
			if args["-v"].(bool) || args["--verbose"].(bool) {
				printFullKeys = true
			}
			err = printKeys(printFullKeys)
		}
	case args["repos"].(bool):
		err = listUserRepos(args["<username>"])
	case args["public"].(bool):
		err = listPubRepos()
	}
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

}
