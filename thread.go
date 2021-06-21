package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"9fans.net/go/acme"
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
	Tree(int, *IDMap) ([]string, error)

	// PreOrder returns a traversal of the thread entry in pre-order.
	PreOrder() []TagSet
}

// A thread is a list of child threads or messages
type Thread []ThreadEntry

func (t Thread) Tree(indent int, m *IDMap) ([]string, error) {
	var entries []string

	for _, e := range t {
		child, err := e.Tree(indent+1, m)
		if err != nil {
			return nil, err
		}

		entries = append(entries, child...)
	}

	return entries, nil
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

func (t ThreadMessage) Tree(indent int, m *IDMap) ([]string, error) {
	subject := t.Headers["Subject"]
	if len(subject) > _maxSubjectLen {
		subject = subject[:_maxSubjectLen] + "..."
	}

	is := strings.Repeat(" ", indent)

	id := m.Put(t.ID)

	mailFrom := t.Headers["From"]
	fromAddr, err := mail.ParseAddress(mailFrom)
	if err != nil {
		return nil, fmt.Errorf("can't parse From header %q: %w", mailFrom, err)
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

	return res, nil
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

func refreshThread(win *acme.Win, threadID string) (IDMap, error) {
	err := win.Fprintf("data", "Looking for thread %s", threadID)
	if err != nil {
		return IDMap{}, err
	}

	cmd := exec.Command("notmuch", "show", "--body=false", "--format=json", "thread:"+threadID)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return IDMap{}, fmt.Errorf("getting output from notmuch: %w", err)
	}

	var thread Thread

	err = json.Unmarshal(output, &thread)
	if err != nil {
		return IDMap{}, fmt.Errorf("unmarshaling thread %s: %w", threadID, err)
	}

	win.Clear()

	idMap := IDMap{Prefix: "msg_"}

	entries, err := thread.Tree(0, &idMap)
	if err != nil {
		return IDMap{}, fmt.Errorf("rendering thread %s: %w", threadID, err)
	}

	// longestcommon.TrimPrefix(entries)
	win.PrintTabbed(strings.Join(entries, "\n"))

	err = winClean(win)
	if err != nil {
		return IDMap{}, fmt.Errorf("cleaning window state: %w", err)
	}

	return idMap, nil
}

var errNoMessage = errors.New("no such message")

// Handle 'look' command or event with given text. Returns an error if the given text does not match a
// message ID and the event that this look was called for should be sent back to Acme
func look(wg *sync.WaitGroup, win *acme.Win, ids IDMap, text string) error {
	id := strings.Trim(text, " \r\t\n")

	if !strings.HasPrefix(id, ids.Prefix) {
		return errNoMessage
	}

	// Get message ID. If we don't have any, push the event back to ACME
	id, err := ids.Get(id)
	if err != nil {
		return errNoMessage
	}

	wg.Add(1)
	// Open thread in new window
	go displayMessage(wg, string(id))

	return nil
}

func displayThread(wg *sync.WaitGroup, threadID string) {
	defer wg.Done()

	win, err := newWin("/Mail/thread/"+threadID, "Get")
	if err != nil {
		win.Errf("can't open thread display window for %s: %s", threadID, err)
		return
	}

	idMap, err := refreshThread(win, threadID)
	if err != nil {
		win.Errf("can't refresh thread display for %s: %s", threadID, err)
		return
	}

	// Events:
	// - l/L:
	//   - look up message with id in evt.Text
	// Commands:
	// - Look:
	//   - show message for ID under cursor (msg_XYZ) (-> evt.Loc)
	//   - or run regular Acme Look command if not on message ID
	// - Get: refresh thread view

	for evt := range win.EventChan() {
		var (
			doLook   bool
			lookText string
		)

		switch evt.C2 {
		case 'x', 'X':
			switch string(evt.Text) {
			case "Get":
				idMap, err = refreshThread(win, threadID)
				if err != nil {
					win.Errf("can't refresh thread display for %s: %s", threadID, err)
				}
				continue
			case "Look":
				doLook = true
				if string(evt.Loc) != "" {
					floc := strings.Split(string(evt.Loc), ":")
					if len(floc) != 2 {
						win.Errf("weird location: %q", evt.Loc)
						continue
					}

					err = win.Addr(floc[1])
					if err != nil {
						win.Errf("can't set address: %s", err)
						continue
					}

					data, err := win.ReadAll("xdata")
					if err != nil {
						win.Errf("can't read data: %s", err)
					}

					lookText = string(data)
				} else {
					lookText = win.Selection()
				}
			default:
				// Let ACME handle the event
				err := win.WriteEvent(evt)
				if err != nil {
					win.Errf("can't write event: %s", err)
					return
				}
			}
		case 'l', 'L':
			doLook = true
			lookText = string(evt.Text)
		}

		if !doLook {
			// Let ACME handle the event
			win.WriteEvent(evt)
			continue
		}

		err := look(wg, win, idMap, lookText)
		switch err {
		case nil:
		case errNoMessage:
			// Doesn't look like a message ID, send it back to ACME
			err := win.WriteEvent(evt)
			if err != nil {
				win.Errf("can't write event: %s", err)
				continue
			}
		default:
			win.Errf("lookup failed: %s", err)
		}
	}
}
