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
	"flag"
	"log"
	"sync"
)

const _maxSubjectLen = 60

var (
	_mainWG sync.WaitGroup
	_query  string
)

func init() {
	flag.StringVar(&_query, "query", "tag:unread and not tag:openbsd", "initial query")
}

func main() {
	flag.Parse()

	log.Println("here we go")

	_mainWG.Add(1)

	err := displayQueryResult(&_mainWG, _query)
	if err != nil {
		log.Panicf("can't run query: %s", err)
	}

	_mainWG.Wait()
	log.Println("bye")
}
