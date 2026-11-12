package llm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"strings"
	"testing"
)

// frame builds an event stream frame from an already encoded header block.
func frame(hb, payload []byte) []byte {
	total := 12 + len(hb) + len(payload) + 4
	out := binary.BigEndian.AppendUint32(nil, uint32(total))  //nolint:gosec // test frames are small
	out = binary.BigEndian.AppendUint32(out, uint32(len(hb))) //nolint:gosec // test frames are small
	out = binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
	out = append(out, hb...)
	out = append(out, payload...)
	return binary.BigEndian.AppendUint32(out, crc32.ChecksumIEEE(out))
}

// strHeader encodes one string header (type 7).
func strHeader(name, value string) []byte {
	b := append([]byte{byte(len(name))}, name...)
	b = append(b, 7)
	b = binary.BigEndian.AppendUint16(b, uint16(len(value))) //nolint:gosec // test values are short
	return append(b, value...)
}

// eventFrame is a Converse stream event: message-type event, the event type and a JSON payload.
func eventFrame(kind, payload string) []byte {
	hb := append(strHeader(":event-type", kind), strHeader(":content-type", "application/json")...)
	hb = append(hb, strHeader(":message-type", "event")...)
	return frame(hb, []byte(payload))
}

// exceptionFrame is a stream error such as throttlingException.
func exceptionFrame(kind, payload string) []byte {
	hb := append(strHeader(":exception-type", kind), strHeader(":message-type", "exception")...)
	return frame(hb, []byte(payload))
}

func TestReadEventMessageDecodesHeadersOfEveryType(t *testing.T) {
	var hb []byte
	hb = append(hb, 1, 'a', 0)                         // true
	hb = append(hb, 1, 'b', 1)                         // false
	hb = append(hb, 1, 'c', 2, 9)                      // byte
	hb = append(hb, 1, 'd', 3, 0, 9)                   // int16
	hb = append(hb, 1, 'e', 4, 0, 0, 0, 9)             // int32
	hb = append(hb, 1, 'f', 5, 0, 0, 0, 0, 0, 0, 0, 9) // int64
	hb = append(hb, 1, 'g', 6, 0, 2, 'h', 'i')         // bytes
	hb = append(hb, 1, 'h', 8, 0, 0, 0, 0, 0, 0, 0, 9) // timestamp
	hb = append(hb, 1, 'i', 9)                         // uuid
	hb = append(hb, make([]byte, 16)...)
	hb = append(hb, strHeader(":event-type", "messageStart")...)
	m, err := readEventMessage(bytes.NewReader(frame(hb, []byte(`{"role":"assistant"}`))))
	if err != nil {
		t.Fatal(err)
	}
	if m.Headers[":event-type"] != "messageStart" || string(m.Payload) != `{"role":"assistant"}` {
		t.Fatalf("got %v %q", m.Headers, m.Payload)
	}
}

func TestReadEventMessageSequenceAndEOF(t *testing.T) {
	r := bytes.NewReader(append(eventFrame("a", `{}`), eventFrame("b", `{"x":1}`)...))
	for _, want := range []string{"a", "b"} {
		m, err := readEventMessage(r)
		if err != nil || m.Headers[":event-type"] != want {
			t.Fatalf("%s: %v %v", want, m, err)
		}
	}
	if _, err := readEventMessage(r); !errors.Is(err, io.EOF) {
		t.Fatalf("want io.EOF at the end, got %v", err)
	}
}

func TestReadEventMessageRejectsCorruptFrames(t *testing.T) {
	good := eventFrame("a", `{"k":"v"}`)
	flip := func(i int) []byte {
		b := append([]byte(nil), good...)
		b[i] ^= 0xff
		return b
	}
	huge := binary.BigEndian.AppendUint32(nil, 1<<30)
	huge = binary.BigEndian.AppendUint32(huge, 0)
	huge = binary.BigEndian.AppendUint32(huge, crc32.ChecksumIEEE(huge))
	cases := map[string]struct {
		in   []byte
		want string
	}{
		"prelude crc":   {flip(9), "prelude checksum"},
		"message crc":   {flip(len(good) - 1), "message checksum"},
		"payload flip":  {flip(len(good) - 6), "message checksum"},
		"short prelude": {good[:5], "truncated frame prelude"},
		"short body":    {good[:len(good)-3], "truncated frame"},
		"huge length":   {huge, "bad frame lengths"},
		"bad header":    {frame([]byte{5, 'a'}, nil), "malformed headers"},
		"bad type":      {frame([]byte{1, 'a', 42}, nil), "unknown header type"},
		"short value":   {frame([]byte{1, 'a', 7, 0}, nil), "malformed headers"},
		"short fixed":   {frame([]byte{1, 'a', 4, 0}, nil), "malformed headers"},
	}
	for name, c := range cases {
		_, err := readEventMessage(bytes.NewReader(c.in))
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want %q", name, err, c.want)
		}
	}
}
