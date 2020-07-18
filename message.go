package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"mime"
	"mime/multipart"
	"net/mail"
	"os/exec"
	"strings"
	"sync"
	"time"

	"9fans.net/go/acme"
)

func writeMessageBody(win *acme.Win, msg *mail.Message) error {
	mediaType, mediaParams, err := mime.ParseMediaType(msg.Header.Get("content-type"))
	if err != nil {
		log.Printf("can't determine media type. using text/plain: %s", err)
		mediaType = "text/plain"
	}

	log.Println("mediatype", mediaType)
	log.Println("params", mediaParams)

	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(msg.Body, mediaParams["boundary"])

		for {
			p, err := reader.NextPart()

			if err == io.EOF {
				break
			}

			if err != nil {
				return err
			}

			body, err := ioutil.ReadAll(p)
			if err != nil {
				return err
			}

			err = win.Fprintf("body", "\nPart headers: %v\n\n%s", p.Header, body)
			if err != nil {
				return err
			}
		}

		return nil
	}

	body, err := ioutil.ReadAll(msg.Body)
	if err != nil {
		return err
	}

	err = win.Fprintf("body", "\n%s", body)
	if err != nil {
		return err
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

	cmd := exec.Command("notmuch", "show", "--format=raw", "id:"+messageID)

	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("can't get message %s: %s", messageID, err)
		return
	}

	win.Clear()

	msg, err := mail.ReadMessage(bytes.NewBuffer(output))
	if err != nil {
		// Parsing failed, just dump the content out as is
		prefix := "Can't parse message:"
		err = win.Fprintf("data", "%s: %s\n\n%s", prefix, err, output)
		if err != nil {
			log.Printf("can't write data to window: %s", err)
			return
		}
	} else {
		log.Println("Headers:", msg.Header)

		var errs []error

		date, err := msg.Header.Date()
		if err != nil {
			errs = append(errs, fmt.Errorf("can't read date: %w", err))
			date = time.Unix(0, 0)
		}

		headers := []string{"Date:\t" + date.Format(time.RFC3339)}

		addrHeaders := []string{"from", "to", "cc", "bcc"}
		for _, hdr := range addrHeaders {
			addrs, err := msg.Header.AddressList(hdr)
			if err != nil {
				if err == mail.ErrHeaderNotPresent {
					continue
				}

				log.Printf("can't read address header %q: %s", hdr, err)
				return
			}

			var vals []string

			for _, addr := range addrs {
				vals = append(vals, addr.String())
			}

			headers = append(headers, strings.Title(hdr)+":\t"+strings.Join(vals, ", "))
		}

		moreHeaders := []string{"reply-to", "list-id", "x-bogosity", "content-type", "subject"}
		for _, hdr := range moreHeaders {
			val := msg.Header.Get(hdr)

			if val == "" {
				continue
			}

			headers = append(headers, strings.Title(hdr)+":\t"+val)
		}

		if len(errs) != 0 {
			err = win.Fprintf("body", "Errors during processing:\n")
			if err != nil {
				log.Printf("can't write data to window: %s", err)
				return
			}
			for _, err := range errs {
				err = win.Fprintf("body", "%s\n", err.Error())
				if err != nil {
					log.Printf("can't write data to window: %s", err)
					return
				}
			}
		}

		win.PrintTabbed(strings.Join(headers, "\n"))

		err = writeMessageBody(win, msg)
		if err != nil {
			log.Printf("can't write message body: %s", err)
			return
		}
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
