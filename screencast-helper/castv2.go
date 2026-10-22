package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	nsConnection = "urn:x-cast:com.google.cast.tp.connection"
	nsHeartbeat  = "urn:x-cast:com.google.cast.tp.heartbeat"
	nsReceiver   = "urn:x-cast:com.google.cast.receiver"
	nsWebrtc     = "urn:x-cast:com.google.cast.webrtc"
	senderID     = "sender-0"
	platformID   = "receiver-0"
)

type castMsg struct {
	src, dst, ns string
	payload      []byte
}

// castConn is a Cast v2 control channel: TLS to port 8009, each message a
// length-prefixed CastMessage protobuf carrying a JSON payload.
type castConn struct {
	conn net.Conn
	mu   sync.Mutex
	msgs chan castMsg
	once sync.Once
	done chan struct{}
}

func (c *castConn) Close() {
	c.once.Do(func() {
		close(c.done)
		c.conn.Close()
	})
}

func dialCast(addr string) (*castConn, error) {
	// Receivers present self-signed device certs; mirroring doesn't need device auth.
	conn, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", addr,
		&tls.Config{InsecureSkipVerify: true})
	if err != nil {
		return nil, err
	}
	c := &castConn{conn: conn, msgs: make(chan castMsg, 64), done: make(chan struct{})}
	go c.readLoop()
	go func() {
		t := time.NewTicker(5 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-c.done:
				return
			case <-t.C:
				if c.send(platformID, nsHeartbeat, map[string]string{"type": "PING"}) != nil {
					return
				}
			}
		}
	}()
	return c, nil
}

func appendField(b []byte, field int, wire int) []byte {
	return binary.AppendUvarint(b, uint64(field<<3|wire))
}

func appendString(b []byte, field int, s string) []byte {
	b = appendField(b, field, 2)
	b = binary.AppendUvarint(b, uint64(len(s)))
	return append(b, s...)
}

func (c *castConn) send(dst, ns string, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	// CastMessage is proto2 with required fields, so protocol_version (1) and
	// payload_type (5) go on the wire even though both are 0.
	var b []byte
	b = appendField(b, 1, 0)
	b = append(b, 0)
	b = appendString(b, 2, senderID)
	b = appendString(b, 3, dst)
	b = appendString(b, 4, ns)
	b = appendField(b, 5, 0)
	b = append(b, 0)
	b = appendString(b, 6, string(payload))

	frame := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(b)), uint32(len(b)))
	frame = append(frame, b...)
	if ns != nsHeartbeat {
		debugf("cast → %s %s %s", dst, ns, payload)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err = c.conn.Write(frame)
	return err
}

func (c *castConn) readLoop() {
	defer close(c.msgs)
	r := bufio.NewReader(c.conn)
	for {
		var size uint32
		if err := binary.Read(r, binary.BigEndian, &size); err != nil {
			log.Printf("cast channel closed: %v", err)
			return
		}
		if size > 64<<10 {
			log.Printf("cast message too large: %d", size)
			return
		}
		body := make([]byte, size)
		if _, err := io.ReadFull(r, body); err != nil {
			log.Printf("cast channel closed: %v", err)
			return
		}
		m, err := parseCastMessage(body)
		if err != nil {
			log.Printf("bad cast message: %v", err)
			continue
		}
		if m.ns == nsHeartbeat {
			var h struct{ Type string }
			if json.Unmarshal(m.payload, &h) == nil && h.Type == "PING" {
				c.send(m.src, nsHeartbeat, map[string]string{"type": "PONG"})
			}
			continue
		}
		select {
		case c.msgs <- m:
		case <-c.done:
			return
		}
	}
}

func parseCastMessage(b []byte) (castMsg, error) {
	var m castMsg
	for len(b) > 0 {
		tag, n := binary.Uvarint(b)
		if n <= 0 {
			return m, errors.New("bad tag")
		}
		b = b[n:]
		field, wire := int(tag>>3), int(tag&7)
		switch wire {
		case 0:
			_, n = binary.Uvarint(b)
			if n <= 0 {
				return m, errors.New("bad varint")
			}
			b = b[n:]
		case 2:
			l, n := binary.Uvarint(b)
			if n <= 0 || uint64(len(b)-n) < l {
				return m, errors.New("bad length")
			}
			v := b[n : n+int(l)]
			b = b[n+int(l):]
			switch field {
			case 2:
				m.src = string(v)
			case 3:
				m.dst = string(v)
			case 4:
				m.ns = string(v)
			case 6, 7:
				m.payload = v
			}
		default:
			return m, fmt.Errorf("unsupported wire type %d", wire)
		}
	}
	return m, nil
}

// await returns the first message on ns whose payload satisfies match.
func (c *castConn) await(ctx context.Context, ns string, timeout time.Duration, match func([]byte) bool) ([]byte, error) {
	deadline := time.After(timeout)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case m, ok := <-c.msgs:
			if !ok {
				return nil, errors.New("cast channel closed")
			}
			if m.ns == ns && match(m.payload) {
				return m.payload, nil
			}
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting on %s", ns)
		}
	}
}
