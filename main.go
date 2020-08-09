package main

/* Plan:
- Three layers of detail:
	- query result -> shows list of threads with match/unmatch, subject, tags
		- Get refreshes result
	- thread display -> expanded view of thread, with mail subjects, indent, and so on
		- Get refreshes result
	- mail display -> one individual message
		- also removes tag:unread from the message
		- somehow make attachments visible
			- handle multipart messages
		- handle GPG and S/MIME
		- "Next in thread" command
- one window per $thing: main view (unread mail), query list, results of query
	- "view thread" is just result of query
	- main view also result of "default" query for `tag:unread`
	- "read mail": result of notmuch show?, except that it also removes the "unread" tag
- "delete" just adds a "deleted" tag
	- special case of tagging
- window tag shows query used to create window?
*/

import (
	"errors"
	"flag"
	"log"
	"strings"
	"sync"

	"9fans.net/go/acme"
)

var (
	_query string
)

func init() {
	flag.StringVar(&_query, "query", "tag:unread and not tag:openbsd", "initial query")
}

func newWin(name string) (*acme.Win, error) {
	win, err := acme.New()
	if err != nil {
		return nil, err
	}

	err = win.Name(name)
	if err != nil {
		return nil, err
	}

	err = win.Fprintf("tag", "Query Compose ")
	if err != nil {
		return nil, err
	}

	return win, nil
}

// winClean clears win's "dirty" flag and jumps to :0.
func winClean(win *acme.Win) error {
	err := win.Ctl("clean")
	if err != nil {
		return err
	}

	err = win.Addr("0")
	if err != nil {
		return err
	}

	err = win.Ctl("dot=addr")
	if err != nil {
		return err
	}

	err = win.Ctl("show")
	if err != nil {
		return err
	}

	return nil
}

var errNotACommand = errors.New("not a command event")

func getCommandArgs(evt *acme.Event) (string, string) {
	cmd := strings.TrimSpace(string(evt.Text))
	arg := strings.TrimSpace(string(evt.Arg))

	if arg == "" {
		parts := strings.SplitN(cmd, " ", 2)

		if len(parts) != 2 {
			arg = ""
		} else {
			arg = parts[1]
		}

		cmd = strings.TrimSpace(parts[0])
	}

	return cmd, arg
}

func handleCommand(wg *sync.WaitGroup, win *acme.Win, evt *acme.Event) error {
	cmd, arg := getCommandArgs(evt)

	switch {
	case cmd == "Query":
		wg.Add(1)

		go func() {
			err := displayQueryResult(wg, arg)
			if err != nil {
				win.Errf("can't display query results for %q: %s", arg, err)
			}
		}()

		return nil
	case cmd == "Compose":
		wg.Add(1)
		go composeMessage(wg, newMailTemplate)
	}

	return errNotACommand
}

func main() {
	flag.Parse()

	var wg sync.WaitGroup
	wg.Add(1)

	err := displayQueryResult(&wg, _query)
	if err != nil {
		log.Panicf("can't run query: %s", err)
	}

	wg.Wait()
	log.Println("bye")
}
