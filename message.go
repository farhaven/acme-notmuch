package main

import (
	"log"
	"os/exec"

	"9fans.net/go/acme"
)

func displayMessage(messageID string) {
	// TODO:
	// - MIME multipart
	// - Handle HTML mail
	// - "Attachments" command
	//   - opens a new window with the attachments (MIME parts) listed, allows saving them somewhere
	// - Only show interesting headers by default
	//   - To, From, Date, Cc, Bcc, Reply-To
	//   - Also show tags

	win, err := acme.New()
	if err != nil {
		log.Printf("can't open message display window for %s: %s", messageID, err)
		return
	}

	err = win.Name("Mail/thread/%s", messageID)
	if err != nil {
		log.Printf("can't set window name for %s: %s", messageID, err)
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

	err = win.Fprintf("data", "%s", output)
	if err != nil {
		log.Printf("can't write data to window: %s", err)
		return
	}

	err = win.Ctl("clean")
	if err != nil {
		log.Printf("can't clean window state: %s", err)
		return
	}

	// TODO: Listen for some commands
}
