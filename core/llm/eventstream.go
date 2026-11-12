package llm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
)

// The AWS event stream framing (application/vnd.amazon.eventstream) that ConverseStream answers with. A message is
//
//	total length (4) | headers length (4) | CRC32 of those 8 bytes (4) | headers | payload | CRC32 of everything before it (4)
//
// all integers big-endian. Each header is name length (1), name, value type (1) and a value whose size the type gives.

// maxEventSize bounds one frame so that a corrupt length cannot allocate gigabytes. AWS limits a frame to 24 MB.
const maxEventSize = 32 << 20

// eventMessage is one decoded frame. Headers holds the string-valued headers (":message-type", ":event-type", ":exception-type", ...).
type eventMessage struct {
	Headers map[string]string
	Payload []byte
}

// readEventMessage reads one frame. It returns io.EOF at a clean end of the stream, and a descriptive error for a truncated or corrupt one.
func readEventMessage(r io.Reader) (*eventMessage, error) {
	var prelude [12]byte
	if _, err := io.ReadFull(r, prelude[:]); err != nil {
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, errors.New("event stream: truncated frame prelude")
		}
		return nil, err // io.EOF between frames
	}
	total, hlen := binary.BigEndian.Uint32(prelude[0:4]), binary.BigEndian.Uint32(prelude[4:8])
	if crc32.ChecksumIEEE(prelude[:8]) != binary.BigEndian.Uint32(prelude[8:12]) {
		return nil, errors.New("event stream: prelude checksum mismatch")
	}
	if total < 16 || total > maxEventSize || uint64(hlen) > uint64(total)-16 {
		return nil, fmt.Errorf("event stream: bad frame lengths (total %d, headers %d)", total, hlen)
	}
	rest := make([]byte, total-12)
	if _, err := io.ReadFull(r, rest); err != nil {
		return nil, errors.New("event stream: truncated frame")
	}
	crc := crc32.NewIEEE()
	_, _ = crc.Write(prelude[:])
	_, _ = crc.Write(rest[:len(rest)-4])
	if crc.Sum32() != binary.BigEndian.Uint32(rest[len(rest)-4:]) {
		return nil, errors.New("event stream: message checksum mismatch")
	}
	headers, err := parseEventHeaders(rest[:hlen])
	if err != nil {
		return nil, err
	}
	return &eventMessage{Headers: headers, Payload: rest[hlen : len(rest)-4]}, nil
}

// parseEventHeaders reads the header block. Only string headers are kept; the other value types are skipped by size.
func parseEventHeaders(b []byte) (map[string]string, error) {
	out := map[string]string{}
	bad := errors.New("event stream: malformed headers")
	for len(b) > 0 {
		n := int(b[0])
		if len(b) < 1+n+1 {
			return nil, bad
		}
		name, typ := string(b[1:1+n]), b[1+n]
		b = b[2+n:]
		size := 0
		switch typ {
		case 0, 1: // true, false: no value
		case 2:
			size = 1
		case 3:
			size = 2
		case 4:
			size = 4
		case 5, 8:
			size = 8
		case 9:
			size = 16
		case 6, 7: // byte array, string: a two-byte length first
			if len(b) < 2 {
				return nil, bad
			}
			size = 2 + int(binary.BigEndian.Uint16(b))
		default:
			return nil, fmt.Errorf("event stream: unknown header type %d", typ)
		}
		if len(b) < size {
			return nil, bad
		}
		if typ == 7 {
			out[name] = string(b[2:size])
		}
		b = b[size:]
	}
	return out, nil
}
