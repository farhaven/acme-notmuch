package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/mattermost/html2text"
	"github.com/pkg/errors"
)

type MessagePartContent interface {
	Render() string
}

type MessagePartContentText struct {
	Text      string
	StripHTML bool
}

func (m *MessagePartContentText) UnmarshalJSON(data []byte) error {
	var s string

	err := json.Unmarshal(data, &s)
	if err != nil {
		return err
	}

	m.Text = s
	m.StripHTML = false

	return nil
}

func (m MessagePartContentText) Render() string {
	if m.StripHTML {
		txt, err := html2text.FromString(m.Text)
		if err != nil {
			log.Printf("can't strip HTML tags: %s", err)
			return m.Text
		}

		return txt
	}

	return m.Text
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

	for k, v := range m.Headers {
		ret = append(ret, k+":\t"+v)
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

	showIdx := 0

	for idx, part := range m {
		if part.ContentType == "text/html" {
			showIdx = idx
			break
		}
	}

	return m[showIdx].Render()
}

type MessagePart struct {
	ID                      int
	ContentType             string `json:"content-type"`
	Content                 MessagePartContent
	ContentDisposition      string `json:"content-disposition"`
	Filename                string
	ContentLength           int    `json:"content-length"`
	ContentTransferEncoding string `json:"content-transfer-encoding"`
}

func (m MessagePart) Render() string {
	if m.ContentDisposition == "attachment" {
		return "Attachment: " + m.Filename
	}

	if m.Content == nil {
		return ""
	}

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

	switch partial.ContentType {
	case "multipart/mixed", "multipart/signed", "multipart/encrypted", "multipart/related":
		var content MessagePartContentMultipartMixed

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		m.Content = content

		return nil
	case "text/plain", "text/html", "application/pkcs7-signature", "application/pgp-signature", "application/pgp-encrypted", "text/rfc822-headers":
		if len(partial.Content) == 0 {
			m.Content = MessagePartContentText{}
			return nil
		}

		var content MessagePartContentText

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		if partial.ContentType == "text/html" {
			content.StripHTML = true
		}

		m.Content = content

		return nil
	case "message/rfc822":
		var content MessagePartMultipleRFC822

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		m.Content = content

		return nil
	case "multipart/alternative":
		var content MessagePartMultipartAlternative

		err := json.Unmarshal(partial.Content, &content)
		if err != nil {
			return errors.Wrapf(err, "parsing %s", partial.ContentType)
		}

		m.Content = content

		return nil
	case "image/jpeg":
		log.Println("skipping image")
		m.Content = nil
		return nil
	}

	log.Printf("decoding: %q", partial.Content)

	return fmt.Errorf("not yet: %s", partial.ContentType)
}

type CryptoState struct {
	Signed struct {
		Encrypted bool
		Headers   []string
		Status    []struct {
			Errors map[string]bool
			KeyID  string
			Status string
		}
	}
	Decrypted struct {
		HeaderMask map[string]string `json:"header-mask"`
		Status     string
	}
}

func (c CryptoState) Render(prefix string) string {
	var res []string

	if c.Signed.Encrypted {
		if len(c.Signed.Headers) > 0 {
			res = append(res, "Signed Headers: "+strings.Join(c.Signed.Headers, ", "))
		}

		for i, s := range c.Signed.Status {
			res = append(res, fmt.Sprintf("Signature Status %d: %v", i, s))
		}
	}

	if c.Decrypted.Status != "" {
		res = append(res, "Decryption Status: "+c.Decrypted.Status)

		if len(c.Decrypted.HeaderMask) > 0 {
			txt := "Decrypted Headers: "

			var hdrs []string
			for h, v := range c.Decrypted.HeaderMask {
				hdrs = append(hdrs, strings.Repeat(" ", len(txt)+1)+h+": "+v)
			}

			if len(hdrs) > 0 {
				res = append(res, txt+strings.TrimSpace(strings.Join(hdrs, "\n\t")))
			}
		}
	}

	if len(res) == 0 {
		return ""
	}

	return "\t" + strings.Join(res, "\n\t")
}

type Root struct {
	MessagePartRFC822

	ID     string
	Crypto CryptoState
	Tags   []string
}

func (m *Root) UnmarshalJSON(data []byte) error {
	// The JSON has a weird layout:
	// - A few layers of nested JSON arrays
	// - Somewhere inside, a JSON object
	// This is likely the result of `notmuch show` expecting to be used to extract complete threads. We're just using it to get single messages though.
	// There is only ever one JSON object in the data, so we get a bit rough and cut everything but the part between the first `{` and the last `}`.
	first := bytes.IndexByte(data, '{')
	last := bytes.LastIndexByte(data, '}')
	if first == -1 || last == -1 {
		return errors.New("can't find object borders")
	}

	data = data[first : last+1]

	// This is a duplicate of the struct layout to prevent recursion into UnmarshalJSON
	var dup struct {
		ID      string
		Crypto  CryptoState
		Tags    []string
		Headers map[string]string
		Body    []MessagePart
	}

	err := json.Unmarshal(data, &dup)
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

func (m Root) Render() string {
	var ret []string

	for _, part := range m.Body {
		ret = append(ret, part.Render())
	}

	return strings.Join(ret, "\n")
}
