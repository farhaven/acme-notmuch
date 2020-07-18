package main

import (
	"bytes"
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

	err = win.Ctl("clean")
	if err != nil {
		log.Printf("can't clean window state: %s", err)
		return
	}

	for evt := range win.EventChan() {
		// Only listen to l and L events to catch right click on a thread ID
		// x and X go right back to acme
		switch evt.C2 {
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
