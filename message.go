package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/mail"
	"os/exec"
	"strings"
	"sync"
	"time"

	"9fans.net/go/acme"
	"github.com/pkg/errors"
)

type MessagePartContent interface {
	Render() string
}

type MessagePartContentText string

func (m MessagePartContentText) Render() string {
	return string(m)
}

type MessagePartContentMultipartMixed []MessagePart

func (m MessagePartContentMultipartMixed) Render() string {
	var ret []string

	for _, part := range m {
		ret = append(ret, part.Render(), "")
	}

	return strings.Join(ret, "\n")
}

type MessagePartRFC822 struct {
	Headers map[string]string
	Body    []MessagePart
}

func (m MessagePartRFC822) Render() string {
	var ret []string

	log.Println("TODO: Better rendering of headers")
	log.Println("TODO: Print separator???")

	for k, v := range m.Headers{
		ret = append(ret, k + ":\t" + v)
	}

	ret = append(ret, "")

	for _, part := range m.Body {
		ret = append(ret, part.Render())
	}

	return strings.Join(ret, "\n")
}

type MessagePartMultipleRFC822 []MessagePartRFC822

func (m MessagePartMultipleRFC822) Render() string {
	var ret []string

	for _, part := range m {
		ret = append(ret, part.Render(), "")
	}

	return strings.Join(ret, "\n")
}

type MessagePartMultipartAlternative []MessagePart
func (m MessagePartMultipartAlternative) Render() string {
	log.Println("TODO: Smarter detection of which part to render")

	textIdx := 0

	for idx, part := range m {
		if part.ContentType == "text/plain" {
			textIdx = idx
			break
		}
	}

	return m[textIdx].Render()
}

type MessagePart struct {
	ID          int
	ContentType string `json:"content-type"`
	Content     MessagePartContent
}

func (m MessagePart) Render() string {
	log.Println("TODO: Render content type")

	return m.Content.Render()
}

func (m *MessagePart) UnmarshalJSON(data []byte) error {
	var partial struct {
		ID          int
		ContentType string `json:"content-type"`
		Content     json.RawMessage
	}

	err := json.Unmarshal(data, &partial)
	if err != nil {
		return errors.Wrap(err, "first stage unwrap")
	}

	m.ID = partial.ID
	m.ContentType = partial.ContentType

	if partial.ContentType == "multipart/mixed" {
		var content MessagePartContentMultipartMixed

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		m.Content = content

		return nil
	}

	if partial.ContentType == "text/plain" || partial.ContentType == "text/html" {
		if len(partial.Content) == 0 {
			m.Content = MessagePartContentText("")
			return nil
		}

		var content MessagePartContentText

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		m.Content = content

		return nil
	}

	if partial.ContentType == "message/rfc822" {
		var content MessagePartMultipleRFC822

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		m.Content = content

		return nil
	}

	if partial.ContentType == "multipart/alternative" {
		var content MessagePartMultipartAlternative

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		m.Content = content

		return nil
	}

	log.Printf("decoding: %q", partial.Content)

	return fmt.Errorf("not yet: %s", partial.ContentType)
}

type MessageRoot struct {
	MessagePartRFC822

	ID     string
	Crypto map[string]interface{} // TODO
	Tags   []string
}

func (m *MessageRoot) UnmarshalJSON(data []byte) error {
	// The JSON has a weird layout:
	// - Two layers of nesting
	// - Inside that, an array with an object and an empty array
	var parts [][][]json.RawMessage

	err := json.Unmarshal(data, &parts)
	if err != nil {
		return errors.Wrap(err, "first stage unwrap")
	}

	if len(parts) != 1 || len(parts[0]) != 1 || len(parts[0][0]) != 2 {
		return errors.New("unexpected part size")
	}

	// This is a duplicate of the struct layout to prevent recursion into UnmarshalJSON
	var dup struct {
		ID      string
		Crypto  map[string]interface{} // TODO
		Tags    []string
		Headers map[string]string
		Body    []MessagePart
	}

	err = json.Unmarshal(parts[0][0][0], &dup)
	if err != nil {
		return err
	}

	m.ID = dup.ID
	m.Crypto = dup.Crypto
	m.Tags = dup.Tags
	m.Headers = dup.Headers
	m.Body = dup.Body

	return nil
}

func (m MessageRoot) Render() string {
	ret := []string{
		"Tags:\t" + strings.Join(m.Tags, ", "),
		"",
		// TODO: Crypto
	}

	log.Println("TODO: Better printing of tag: Join headers before printing")

	for _, part := range m.Body {
		ret = append(ret, part.Render())
	}

	return strings.Join(ret, "\n")
}

func writeMessageBody(win *acme.Win, messageID string) error {
	// TODO: Decode PGP
	// TODO: Handle HTML mail

	cmd := exec.Command("notmuch", "show", "--format=json", "--entire-thread=false", "id:"+messageID)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return errors.Wrap(err, "loading message payload")
	}

	var msg MessageRoot
	err = json.Unmarshal(output, &msg)
	if err != nil {
		return errors.Wrap(err, "decoding payload")
	}

	err = win.Fprintf("body", "%s", msg.Render())
	if err != nil {
		return errors.Wrap(err, "writing body")
	}

	return nil
}

// nextUnread returns the message ID of the next unread message in the same thread as id
func nextUnread(wg *sync.WaitGroup, id string) error {
	// Get thread ID of the given message
	// TODO: Handle multiple threads

	cmd := exec.Command("notmuch", "search", "--format=json", "--output=threads", "id:"+id)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return err
	}

	var threadIDs []string
	err = json.Unmarshal(output, &threadIDs)
	if err != nil {
		return err
	}

	if len(threadIDs) == 0 {
		return errors.New("can't find thread for message")
	} else if len(threadIDs) > 1 {
		return errors.New("more than one thread id for message")
	}

	// Get thread for the message
	cmd = exec.Command("notmuch", "show", "--format=json", "--body=false", "thread:"+threadIDs[0])
	output, err = cmd.CombinedOutput()
	if err != nil {
		return err
	}

	var thread Thread
	err = json.Unmarshal(output, &thread)
	if err != nil {
		return err
	}

	l := thread.PreOrder()

	buf, err := json.MarshalIndent(&l, "", "  ")
	if err != nil {
		return err
	}

	log.Println("pre-order traversal of thread:", string(buf))

	foundThisMsg := false
	foundNextMsg := false
	for idx, entry := range l {
		if entry.MsgID == id {
			log.Println("found message", id, "at index", idx)
			foundThisMsg = true
			continue
		}

		if !foundThisMsg {
			continue
		}

		if entry.Tags["unread"] {
			log.Println("next unread message is:", entry.MsgID, "at", idx)
			wg.Add(1)
			go displayMessage(wg, entry.MsgID)
			foundNextMsg = true
			break
		}
	}

	if !foundThisMsg {
		return errors.New("current message not found in thread")
	}

	if !foundNextMsg {
		return errors.New("no next message found")
	}

	return nil
}

func getAllHeaders(messageID string) (mail.Header, error) {
	cmd := exec.Command("notmuch", "show", "--format=raw", "id:"+messageID)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}

	msg, err := mail.ReadMessage(bytes.NewBuffer(output))
	if err != nil {
		return nil, err
	}

	return msg.Header, nil
}

func writeMessageHeaders(win *acme.Win, messageID string) error {
	allHeaders, err := getAllHeaders(messageID)
	if err != nil {
		return errors.Wrap(err, "getting headers")
	}

	var errs []error

	date, err := allHeaders.Date()
	if err != nil {
		errs = append(errs, fmt.Errorf("can't read date: %w", err))
		date = time.Unix(0, 0)
	}

	headers := []string{"Date:\t" + date.Format(time.RFC3339)}

	addrHeaders := []string{"from", "to", "cc", "bcc"}
	for _, hdr := range addrHeaders {
		addrs, err := allHeaders.AddressList(hdr)
		if err != nil {
			if err == mail.ErrHeaderNotPresent {
				continue
			}

			return errors.Wrap(err, "reading address header")
		}

		var vals []string

		for _, addr := range addrs {
			vals = append(vals, addr.String())
		}

		headers = append(headers, strings.Title(hdr)+":\t"+strings.Join(vals, ", "))
	}

	moreHeaders := []string{"reply-to", "list-id", "x-bogosity", "content-type", "subject"}
	for _, hdr := range moreHeaders {
		val := allHeaders.Get(hdr)

		if val == "" {
			continue
		}

		headers = append(headers, strings.Title(hdr)+":\t"+val)
	}

	if len(errs) != 0 {
		err = win.Fprintf("body", "Errors during processing:\n")
		if err != nil {
			return errors.Wrap(err, "writing to window")
		}
		for _, err := range errs {
			err = win.Fprintf("body", "%s\n", err.Error())
			if err != nil {
				return errors.Wrap(err, "writing to window")
			}
		}
	}

	win.PrintTabbed(strings.Join(headers, "\n"))

	return nil
}

func displayMessage(wg *sync.WaitGroup, messageID string) {
	// TODO:
	// - MIME multipart
	// - Handle HTML mail
	// - "Attachments" command
	//   - opens a new window with the attachments (MIME parts) listed, allows saving them somewhere
	//   - Decode base64
	// - Only show interesting headers by default
	//   - To, From, Date, Cc, Bcc, Reply-To
	//   - Also show tags
	//   - Add "Headers" command to show full list of headers
	// - "Next unread" command for next unread message in thread
	// - Remove "unread" tag from messages

	defer wg.Done()

	win, err := newWin("Mail/message/" + messageID)
	if err != nil {
		log.Printf("can't open message display window for %s: %s", messageID, err)
		return
	}

	err = win.Fprintf("tag", "Next ")
	if err != nil {
		log.Printf("can't write to tag: %s", err)
		return

	}

	err = win.Fprintf("data", "Looking for message %s", messageID)
	if err != nil {
		log.Printf("can't write to body: %s", err)
		return
	}

	win.Clear()

	err = writeMessageHeaders(win, messageID)
	if err != nil {
		log.Printf("can't write headers for %q: %s", messageID, err)
		return
	}

	err = writeMessageBody(win, messageID)
	if err != nil {
		log.Printf("can't write message body: %s", err)
		return
	}

	err = winClean(win)
	if err != nil {
		log.Printf("can't clean window state: %s", err)
		return
	}

	for evt := range win.EventChan() {
		// Only listen to l and L events to catch right click on a thread ID
		// x and X go right back to acme
		switch evt.C2 {
		case 'x', 'X':
			if string(evt.Text) == "Next" {
				log.Println("got Next command")
				err := nextUnread(wg, messageID)
				if err != nil {
					log.Printf("can't jump to next unread message: %s", err)
				}
				continue
			}

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
		case 'l', 'L':
			err := win.WriteEvent(evt)
			if err != nil {
				log.Printf("can't write event: %s", err)
				return
			}

		default:
			continue
		}
	}
}
