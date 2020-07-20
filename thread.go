package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/mail"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// IDMap is used to map message IDs to shorter identifier strings and back.
// This structure and its methods are not goroutine safe.
type IDMap struct {
	Prefix string
	Count  int
	Vals   map[string]string
}

// Put places val in i and returns an identifier that can be used to get val back
func (i *IDMap) Put(val string) string {
	if i.Vals == nil {
		i.Vals = make(map[string]string)
	}

	id := i.Prefix + strconv.Itoa(i.Count)
	i.Count += 1
	i.Vals[id] = val
	return id
}

// Get returns a previously allocated value from i
func (i IDMap) Get(id string) (string, error) {
	val, ok := i.Vals[id]
	if !ok {
		return "", fmt.Errorf("no entry with ID %q", id)
	}

	return val, nil
}

type TagSet struct {
	MsgID string
	Tags  map[string]bool
}

type ThreadEntry interface {
	// Tree renders a tread entry of the given level as a list of strings. It places message IDs in the given IDMap.
	Tree(int, *IDMap) []string

	// PreOrder returns a traversal of the thread entry in pre-order.
	PreOrder() []TagSet
}

// A thread is a list of child threads or messages
type Thread []ThreadEntry

func (t Thread) Tree(indent int, m *IDMap) []string {
	var entries []string

	for _, e := range t {
		entries = append(entries, e.Tree(indent+1, m)...)
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
	if err != nil {
		return err
	}

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

// PreOrder flattens t to pre-order traversal, returning a list of message IDs and tags
func (t Thread) PreOrder() []TagSet {
	var ret []TagSet

	for _, entry := range t {
		ret = append(ret, entry.PreOrder()...)
	}

	return ret
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

func (t ThreadMessage) Tree(indent int, m *IDMap) []string {
	subject := t.Headers["Subject"]
	if len(subject) > _maxSubjectLen {
		subject = subject[:_maxSubjectLen] + "..."
	}

	is := strings.Repeat(" ", indent)

	id := m.Put(t.ID)
	log.Println("msg id:", id, t.ID, "indent:", indent)

	mailFrom := t.Headers["From"]
	fromAddr, err := mail.ParseAddress(mailFrom)
	if err != nil {
		log.Printf("can't parse From header %q: %s", mailFrom, err)
	} else {
		if fromAddr.Name != "" {
			mailFrom = fromAddr.Name
		} else {
			mailFrom = fromAddr.Address
		}
	}

	res := []string{
		id + "\t" + is + subject + "\t" + "(" + mailFrom + ")\t" + fmt.Sprintf("%v", t.Tags),
	}

	log.Println("res", res)

	return res
}

func (t ThreadMessage) PreOrder() []TagSet {
	tags := make(map[string]bool)

	for _, tag := range t.Tags {
		tags[tag] = true
	}

	return []TagSet{
		{MsgID: t.ID, Tags: tags},
	}
}

func displayThread(wg *sync.WaitGroup, threadID string) {
	defer wg.Done()

	win, err := newWin("Mail/thread/" + threadID)
	if err != nil {
		log.Printf("can't open thread display window for %s: %s", threadID, err)
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

	var thread Thread

	err = json.Unmarshal(output, &thread)
	if err != nil {
		log.Printf("can't unmarshal thread %s: %s", threadID, err)
		return
	}

	win.Clear()

	idMap := IDMap{Prefix: "msg_"}

	entries := thread.Tree(0, &idMap)
	// longestcommon.TrimPrefix(entries)
	win.PrintTabbed(strings.Join(entries, "\n"))

	err = winClean(win)
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
		id := string(bytes.Trim(evt.Text, " \r\t\n"))

		log.Printf("looking for id %q", id)

		if !strings.HasPrefix(id, idMap.Prefix) {
			// Doesn't look like a thread ID, send it back to ACME
			err := win.WriteEvent(evt)
			if err != nil {
				log.Printf("can't write event: %s", err)
				return
			}
			continue
		}

		log.Println("looking up message ID")

		// Get message ID. If we don't have any, push the event back to ACME
		id, err := idMap.Get(id)
		if err != nil {
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
