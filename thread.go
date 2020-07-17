package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"9fans.net/go/acme"
	"github.com/jpillora/longestcommon"
)

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

// Message ID: Looks a bit like an email address, with a saner part before the @
// TODO: Check if this is completely correct
var _messageIDRegex = regexp.MustCompile(`[a-zA-Z0-9.-]+@[a-zA-Z0-9.-]+\.[a-z]+`)

func displayThread(wg *sync.WaitGroup, threadID string) {
	defer wg.Done()

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

	err = win.Fprintf("tag", "Query ")
	if err != nil {
		return
	}

	err = win.Fprintf("data", "Looking for thread %s", threadID)
	if err != nil {
		log.Printf("can't write to body: %s", err)
		return
	}

	cmd := exec.Command("notmuch", "show", "--body=false", "--format=json", "thread:"+threadID)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("can't get thread %s: %s", threadID, err)
		return
	}

	log.Printf("got %d bytes of output: %q", len(output), output)

	var thread Thread

	err = json.Unmarshal(output, &thread)
	if err != nil {
		log.Printf("can't unmarshal thread %s: %s", threadID, err)
		return
	}

	win.Clear()

	log.Printf("got thread: %#v", thread)

	entries := thread.Tree(0)
	longestcommon.TrimPrefix(entries)
	win.PrintTabbed(strings.Join(entries, "\n"))

	err = win.Ctl("clean")
	if err != nil {
		log.Printf("can't clean window state: %s", err)
		return
	}

	for evt := range win.EventChan() {
		// Only listen to l and L events to catch right click on a thread ID
		// x and X go right back to acme
		switch evt.C2 {
		case 'l', 'L':
		case 'x', 'X':
			err := handleQueryEvent(wg, evt)
			switch err {
			case nil:
				// Nothing to do, event already handled
			case errNotAQuery:
				// Let ACME handle the event
				err := win.WriteEvent(evt)
				if err != nil {
					return
				}
			default:
				log.Printf("can't handle event: %s", err)
			}

			continue
		default:
			continue
		}

		log.Printf("got 'Look' event: %q", evt.Text)

		// Match message IDs
		id := bytes.Trim(evt.Text, " \r\t\n")

		if !_messageIDRegex.Match(id) {
			// Doesn't look like a thread ID, send it back to ACME
			err := win.WriteEvent(evt)
			if err != nil {
				log.Printf("can't write event: %s", err)
				return
			}
			continue
		}

		wg.Add(1)
		// Open thread in new window
		go displayMessage(wg, string(id))
	}
}
