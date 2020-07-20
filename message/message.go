package message

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/jaytaylor/html2text"
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
		txt, err := html2text.FromString(m.Text, html2text.Options{PrettyTables: true})
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

type Root struct {
	MessagePartRFC822

	ID     string
	Crypto map[string]interface{} // TODO
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
		Crypto  map[string]interface{} // TODO
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
