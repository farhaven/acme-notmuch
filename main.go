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
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"

	"9fans.net/go/acme"
	"github.com/jpillora/longestcommon"
)

const _maxSubjectLen = 60

type ThreadEntry interface {
	Tree(int) []string
}

// A thread is a list of child threads or messages
type Thread []ThreadEntry

func (t Thread) Tree(indent int) []string {
	var entries []string

	for _, e := range t {
		entries = append(entries, e.Tree(indent+1)...)
	}

	return entries
}

func (t *Thread) UnmarshalJSON(data []byte) error {
	// Decode as list of things that are either threads or messages
	var (
		raw     []json.RawMessage
		decoded Thread
	)

	err := json.Unmarshal(data, &raw)

	for _, rawEntry := range raw {
		// Decode the raw message either as a ThreadMessage or as a whole thread
		// Let's try the message first
		var (
			entry ThreadEntry
			tm    ThreadMessage
		)

		err = json.Unmarshal(rawEntry, &tm)
		if err != nil {
			// Can't unmarshal as raw message, let's try the thread
			var te Thread
			err = json.Unmarshal(rawEntry, &te)
			if err != nil {
				return err
			}
			entry = te
		} else {
			entry = tm
		}

		decoded = append(decoded, entry)
	}

	*t = decoded

	return nil
}

type ThreadMessage struct {
	ID           string
	Match        bool
	Excluded     bool
	Filename     []string // May be more than one file for a message, i.e. duplicates
	Timestamp    int      // Unix
	DateRelative string   `json:"date_relative"`
	Tags         []string
	Headers      map[string]string
}

func (t ThreadMessage) Tree(indent int) []string {
	subject := t.Headers["Subject"]
	if len(subject) > _maxSubjectLen {
		subject = subject[:_maxSubjectLen] + "..."
	}

	is := strings.Repeat(" ", indent)

	res := []string{
		is + "(F:" + t.Headers["From"] + ") (S:" + subject + ")" + fmt.Sprintf("%v", t.Tags),
		is + t.ID,
	}

	return res
}

// Thread ID: sequence of 16 hex digits
var _threadIDRegex = regexp.MustCompile("[0-9a-f]{16}")

func displayThread(threadID string) {
	win, err := acme.New()
	if err != nil {
		log.Printf("can't open thread display window for %s: %s", threadID, err)
		return
	}

	err = win.Name("Mail/thread/%s", threadID)
	if err != nil {
		log.Printf("can't set window name for %s: %s", threadID, err)
		return
	}

	err = win.Fprintf("data", "Looking for thread %s", threadID)
	if err != nil {
		log.Printf("can't write to body: %s", err)
		return
	}

	// notmuch show --body=false --format=json thread:0000000000035355
	cmd := exec.Command("notmuch", "show", "--body=false", "--format=json", fmt.Sprintf("thread:%s", threadID))

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("can't get thread %s: %s", threadID, err)
		return
	}

	var thread Thread

	err = json.Unmarshal(output, &thread)
	if err != nil {
		log.Printf("can't unmarshal thread %s: %s", threadID, err)
		return
	}

	win.Clear()

	// TODO: PrintTabbed?
	entries := thread.Tree(0)
	longestcommon.TrimPrefix(entries)
	win.PrintTabbed(strings.Join(entries, "\n"))

	err = win.Ctl("clean")
	if err != nil {
		log.Printf("can't clean window state: %s", err)
		return
	}

	// TODO: Wait for Look on message ID
}

type QueryResult struct {
	Thread       string
	Timestamp    int    // Unix timestamp
	DateRelative string `json:"date_relative"` // Should probably be parsed as a real time?
	Subject      string
	Tags         []string
	Query        []string // Query to run to get this exact thread?
	Matched      int      // How many messages in the thread matched the query
	Total        int      // Total number of messages in the thread?
}

func (q QueryResult) String() string {
	subject := q.Subject
	if len(subject) > _maxSubjectLen {
		subject = subject[:_maxSubjectLen] + "..."
	}

	return fmt.Sprintf("%s\t(%d/%d)\t%s\t%v", q.Thread, q.Matched, q.Total, subject, q.Tags)
}

// displayQueryResult opens a new window that shows the results of query
func displayQueryResult(query string) error {
	win, err := acme.New()
	if err != nil {
		return err
	}

	err = win.Name("Mail/query")
	if err != nil {
		return err
	}

	err = win.Fprintf("data", "Running query %s", query)
	if err != nil {
		return err
	}

	// TODO: Double check output=summary
	cmd := exec.Command("notmuch", "search", "--output=summary", "--format=json", query)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}

	win.Clear()

	var results []QueryResult
	err = json.Unmarshal(output, &results)
	if err != nil {
		return err
	}

	var res []string
	for _, r := range results {
		res = append(res, r.String())
	}

	win.PrintTabbed(strings.Join(res, "\n"))

	err = win.Ctl("clean")
	if err != nil {
		return err
	}

	for evt := range win.EventChan() {
		// Only listen to l and L events to catch right click on a thread ID
		// x and X go right back to acme
		switch evt.C2 {
		case 'l', 'L':
		case 'x', 'X':
			err := win.WriteEvent(evt)
			if err != nil {
				return err
			}
			continue
		default:
			continue
		}

		log.Printf("got 'Look' event: %q", evt.Text)

		// Match thread IDs: Sequence of 16 hex digits, followed by optional whitespace
		id := bytes.Trim(evt.Text, " \r\t\n")

		if !_threadIDRegex.Match(id) {
			// Doesn't look like a thread ID, send it back to ACME
			err := win.WriteEvent(evt)
			if err != nil {
				return err
			}
			continue
		}

		// Open thread in new window
		go displayThread(string(id))
	}

	return nil
}

func main() {
	log.Println("here we go")

	err := displayQueryResult("tag:unread and not tag:openbsd")
	if err != nil {
		log.Panicf("can't run query: %s", err)
	}

	log.Println("bye")
}
