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
)

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

// Thread ID: sequence of 16 hex digits
var _threadIDRegex = regexp.MustCompile("[0-9a-f]{16}")

// displayQueryResult opens a new window that shows the results of query
func displayQueryResult(wg *sync.WaitGroup, query string) error {
	defer wg.Done()

	win, err := acme.New()
	if err != nil {
		return err
	}

	err = win.Name("Mail/query")
	if err != nil {
		return err
	}

	err = win.Fprintf("tag", "Query ")
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
			err := handleQueryEvent(wg, evt)
			switch err {
			case nil:
				// Nothing to do, event already handled
			case errNotAQuery:
				// Let ACME handle the event
				err := win.WriteEvent(evt)
				if err != nil {
					return err
				}
			default:
				log.Printf("can't handle event: %s", err)
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

		wg.Add(1)
		// Open thread in new window
		go displayThread(wg, string(id))
	}

	return nil
}
