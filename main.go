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
		- pass html mail through lynx?
			- via plumber?
		- handle GPG and S/MIME
		- "Next in thread" command
- use notmuch command line tools to do the heavy lifting, with JSON output
- one window per $thing: main view (unread mail), query list, results of query
	- "view thread" is just result of query
	- main view also result of "default" query for `tag:unread`
	- "read mail": result of notmuch show?, except that it also removes the "unread" tag
- "delete" just adds a "deleted" tag
	- special case of tagging
- send using msmtp
- window tag shows query used to create window?
- runs until last window is closed
- starts with single window
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

	err = win.Fprintf("tag", "Query ")
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

var errNotAQuery = errors.New("not a query event")

func handleQueryEvent(wg *sync.WaitGroup, evt *acme.Event) error {
	cmd := strings.TrimSpace(string(evt.Text))
	arg := strings.TrimSpace(string(evt.Arg))

	log.Printf("cmd: %q, arg: %q", cmd, arg)

	if cmd != "Query" && !strings.HasPrefix(cmd, "Query ") {
		return errNotAQuery
	}

	log.Println("discovering args")

	if arg == "" {
		parts := strings.SplitN(cmd, " ", 2)
		log.Printf("parts: %#v", parts)

		if len(parts) != 2 {
			return errNotAQuery
		}

		arg = parts[1]
	}

	log.Printf("got arg: %q", arg)

	go func() {
		err := displayQueryResult(wg, arg)
		if err != nil {
			log.Printf("can't display query results for %q: %s", arg, err)
		}
	}()

	return nil
}

func main() {
	flag.Parse()

	log.Println("here we go")

	var wg sync.WaitGroup
	wg.Add(1)

	err := displayQueryResult(&wg, _query)
	if err != nil {
		log.Panicf("can't run query: %s", err)
	}

	wg.Wait()
	log.Println("bye")
}
