package main

import (
	"encoding/json"
	"io/ioutil"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/assert"
)

func TestMessage_Decode(t *testing.T) {
	body, err := ioutil.ReadFile("test-data/message.json")
	require.NoError(t, err)

	var m MessageRoot
	err = json.Unmarshal(body, &m)

	require.NoError(t, err)

	require.Len(t, m.Body, 1)
	assert.Equal(t, "multipart/mixed", m.Body[0].ContentType)

	require.IsType(t, MessagePartContentMultipartMixed{}, m.Body[0].Content)
	mp0 := m.Body[0].Content.(MessagePartContentMultipartMixed)
	require.Len(t, mp0, 3)

	assert.Equal(t, mp0[0].ContentType, "text/plain")
	assert.Equal(t, mp0[1].ContentType, "message/rfc822")
	assert.Equal(t, mp0[2].ContentType, "message/rfc822")

	require.IsType(t, MessagePartContentText(""), mp0[0].Content)
	require.IsType(t, MessagePartMultipleRFC822{}, mp0[1].Content)
	require.IsType(t, MessagePartMultipleRFC822{}, mp0[2].Content)

	mp01 := mp0[1].Content.(MessagePartMultipleRFC822)
	mp02 := mp0[2].Content.(MessagePartMultipleRFC822)

	require.Len(t, mp01, 1)

	mp010 := mp01[0]
	require.Len(t, mp010.Body, 1)
	require.IsType(t, MessagePartMultipartAlternative{}, mp010.Body[0].Content)
	mp0100 := mp010.Body[0].Content.(MessagePartMultipartAlternative)
	require.Len(t, mp0100, 2)

	require.IsType(t, MessagePartContentText(""), mp0100[0].Content)
	assert.Equal(t, "text/plain", mp0100[0].ContentType)

	require.IsType(t, MessagePartContentText(""), mp0100[1].Content)
	assert.Equal(t, "text/html", mp0100[1].ContentType)

	require.Len(t, mp02, 1)
	// ... this could go on testing properties of mp02, but those are already covered by the first embedded
	// RFC822 message
}
