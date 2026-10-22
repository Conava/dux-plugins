package main

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"fmt"
	"log"
	"math/rand/v2"
	"net"
	"sync"
	"time"
)

const (
	maxPacketSize = 1500 - 20 - 8 // IPv4 UDP on Ethernet
	rtpHeaderSize = 12 + 7        // RTP header + Cast header
	maxPayload    = maxPacketSize - rtpHeaderSize
	maxUnacked    = 100 // frames in flight before deltas get dropped
	ntpUnixOffset = 2208988800
)

type sentFrame struct {
	id       int64
	sent     time.Time
	packets  [][]byte
	lastSent []time.Time
}

// stream sends one media stream in Cast Streaming's RTP profile: each frame is
// AES-128-CTR encrypted, split into packets and kept until the receiver's
// checkpoint passes it, so NACKed packets can be resent.
type stream struct {
	name     string
	conn     *net.UDPConn
	ssrc     uint32
	pt       byte
	rate     int // RTP clock rate
	block    cipher.Block
	ivMask   [16]byte
	start    time.Time
	isVideo  bool
	codec    string
	keyframe func()          // asks the encoder for a key frame, may be nil
	bitrate  *bitrateControl // may be nil

	mu          sync.Mutex
	seq         uint16
	nextID      int64
	checkpoint  int64
	frames      map[int64]*sentFrame
	waitKey     bool
	lastRTP     uint32
	lastRTPTime time.Time
	lastSend    time.Time
	packets     uint32
	octets      uint32
	acked, nack int

	lastStuckLog time.Time
}

func newStream(name string, conn *net.UDPConn, ssrc uint32, pt byte, rate int, key, ivMask [16]byte, start time.Time, isVideo bool) *stream {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		panic(err)
	}
	return &stream{
		name: name, conn: conn, ssrc: ssrc, pt: pt, rate: rate, block: block, ivMask: ivMask,
		start: start, isVideo: isVideo, seq: uint16(rand.Uint32()), checkpoint: -1,
		frames: map[int64]*sentFrame{},
	}
}

// rtpNow is the RTP timestamp for the current moment on this stream's clock.
func (s *stream) rtpNow(now time.Time) uint32 {
	return uint32(now.Sub(s.start).Seconds() * float64(s.rate))
}

func (s *stream) enqueue(data []byte, key bool, rtp uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.isVideo {
		if s.nextID-1-s.checkpoint > maxUnacked && !key {
			if !s.waitKey {
				log.Printf("%s: %d frames unacked, dropping until next key frame", s.name, s.nextID-1-s.checkpoint)
				if s.keyframe != nil {
					s.keyframe()
				}
			}
			s.waitKey = true
		}
		if s.waitKey && !key {
			return
		}
		if key {
			s.waitKey = false
		}
	}

	id := s.nextID
	s.nextID++
	ref := id - 1
	if key || !s.isVideo {
		ref = id
	}

	var nonce [16]byte
	binary.BigEndian.PutUint32(nonce[8:], uint32(id))
	for i := range nonce {
		nonce[i] ^= s.ivMask[i]
	}
	enc := make([]byte, len(data))
	cipher.NewCTR(s.block, nonce[:]).XORKeyStream(enc, data)

	n := max(1, (len(enc)+maxPayload-1)/maxPayload)
	f := &sentFrame{id: id, sent: time.Now(), packets: make([][]byte, n), lastSent: make([]time.Time, n)}
	for i := range n {
		chunk := enc[i*maxPayload : min(len(enc), (i+1)*maxPayload)]
		p := make([]byte, rtpHeaderSize, rtpHeaderSize+len(chunk))
		p[0] = 0x80
		p[1] = s.pt
		if i == n-1 {
			p[1] |= 0x80
		}
		binary.BigEndian.PutUint32(p[4:], rtp)
		binary.BigEndian.PutUint32(p[8:], s.ssrc)
		p[12] = 0x40 // reference frame id present
		if key {
			p[12] |= 0x80
		}
		p[13] = byte(id)
		binary.BigEndian.PutUint16(p[14:], uint16(i))
		binary.BigEndian.PutUint16(p[16:], uint16(n-1))
		p[18] = byte(ref)
		f.packets[i] = append(p, chunk...)
	}
	s.frames[id] = f
	s.lastRTP, s.lastRTPTime = rtp, time.Now()
	for i := range f.packets {
		s.sendPacketLocked(f, i)
	}
}

// sendPacketLocked sends (or resends) one packet with a fresh sequence number.
func (s *stream) sendPacketLocked(f *sentFrame, i int) {
	p := f.packets[i]
	binary.BigEndian.PutUint16(p[2:], s.seq)
	s.seq++
	if _, err := s.conn.Write(p); err != nil {
		log.Printf("%s: send: %v", s.name, err)
	}
	now := time.Now()
	f.lastSent[i] = now
	s.lastSend = now
	s.packets++
	s.octets += uint32(len(p) - rtpHeaderSize)
}

// Feedback carries only the low 8 bits of frame ids. As in Open Screen, the
// checkpoint expands to the newest id at or below the last frame sent, and
// loss and ACK entries to the first id above the checkpoint.
func expandAtMost(low byte, limit int64) int64 {
	id := limit&^0xff | int64(low)
	if id > limit {
		id -= 256
	}
	return id
}

func expandAbove(low byte, base int64) int64 {
	id := base&^0xff | int64(low)
	if id <= base {
		id += 256
	}
	return id
}

func (s *stream) onFeedback(checkpoint byte, losses [][3]int, received []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ck := expandAtMost(checkpoint, s.nextID-1)
	if ck == s.checkpoint && time.Since(s.lastStuckLog) > time.Second && s.nextID-1 > ck {
		s.lastStuckLog = time.Now()
		_, have := s.frames[ck+1]
		debugf("%s: stuck at checkpoint %d (next frame held: %v, newest %d), losses %v, also received %v",
			s.name, ck, have, s.nextID-1, losses, received)
	}
	for _, off := range received {
		if f := s.frames[ck+off]; f != nil {
			delete(s.frames, ck+off)
		}
	}
	for id := s.checkpoint + 1; id <= ck; id++ {
		if _, ok := s.frames[id]; ok {
			delete(s.frames, id)
			s.acked++
		}
	}
	s.checkpoint = max(s.checkpoint, ck)

	now := time.Now()
	for _, l := range losses {
		f := s.frames[expandAbove(byte(l[0]), ck)]
		if f == nil {
			continue
		}
		resend := func(i int) {
			// A NACK can cross a retransmission already in flight.
			if i < len(f.packets) && now.Sub(f.lastSent[i]) > 20*time.Millisecond {
				s.sendPacketLocked(f, i)
				s.nack++
			}
		}
		if l[1] == 0xffff {
			for i := range f.packets {
				resend(i)
			}
			continue
		}
		resend(l[1])
		for bit := range 8 {
			if l[2]&(1<<bit) != 0 {
				resend(l[1] + 1 + bit)
			}
		}
	}
}

func (s *stream) onPictureLoss() {
	log.Printf("%s: receiver reports picture loss", s.name)
	if s.keyframe != nil {
		s.keyframe()
	}
}

// tick sends the periodic Sender Report, without which receivers don't render,
// and a kickstart packet when the newest frame looks lost entirely.
func (s *stream) tick(now time.Time, report bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastRTPTime.IsZero() {
		return
	}
	if report {
		sr := make([]byte, 28)
		sr[0] = 0x80
		sr[1] = 200
		binary.BigEndian.PutUint16(sr[2:], 28/4-1)
		binary.BigEndian.PutUint32(sr[4:], s.ssrc)
		binary.BigEndian.PutUint64(sr[8:], ntp(now))
		rtp := s.lastRTP + uint32(now.Sub(s.lastRTPTime).Seconds()*float64(s.rate))
		binary.BigEndian.PutUint32(sr[16:], rtp)
		binary.BigEndian.PutUint32(sr[20:], s.packets)
		binary.BigEndian.PutUint32(sr[24:], s.octets)
		if _, err := s.conn.Write(sr); err != nil {
			log.Printf("%s: sender report: %v", s.name, err)
		}
	}
	if f := s.frames[s.checkpoint+1]; f != nil && s.bitrate != nil {
		s.bitrate.update(now, now.Sub(f.sent))
	} else if s.bitrate != nil {
		s.bitrate.update(now, 0)
	}
	newest := s.nextID - 1
	if f := s.frames[newest]; f != nil && newest > s.checkpoint && now.Sub(s.lastSend) > 250*time.Millisecond {
		s.sendPacketLocked(f, len(f.packets)-1)
	}
}

func (s *stream) stats() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return fmt.Sprintf("%s: sent %d frames, acked %d, in flight %d, resent %d packets",
		s.name, s.nextID, s.acked, s.nextID-1-s.checkpoint, s.nack)
}

func ntp(t time.Time) uint64 {
	secs := uint64(t.Unix()) + ntpUnixOffset
	frac := uint64(t.Nanosecond()) << 32 / 1e9
	return secs<<32 | frac
}

// readRTCP dispatches receiver feedback to the stream it is about.
func readRTCP(conn *net.UDPConn, streams map[uint32]*stream) {
	buf := make([]byte, 2048)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			log.Printf("rtcp: %v", err)
			return
		}
		parseRTCP(buf[:n], streams)
	}
}

func parseRTCP(b []byte, streams map[uint32]*stream) {
	for len(b) >= 4 {
		if b[0]>>6 != 2 {
			return
		}
		sub, pt := b[0]&0x1f, b[1]
		size := (int(binary.BigEndian.Uint16(b[2:])) + 1) * 4
		if size > len(b) {
			return
		}
		pkt := b[:size]
		b = b[size:]
		if pt != 206 || len(pkt) < 12 {
			continue
		}
		s := streams[binary.BigEndian.Uint32(pkt[8:])]
		if s == nil {
			continue
		}
		switch sub {
		case 1:
			s.onPictureLoss()
		case 15:
			if len(pkt) < 20 || string(pkt[12:16]) != "CAST" {
				continue
			}
			count := int(pkt[17])
			var losses [][3]int
			off := 20
			for i := 0; i < count && off+4 <= len(pkt); i++ {
				l := pkt[off:]
				losses = append(losses, [3]int{int(l[0]), int(binary.BigEndian.Uint16(l[1:])), int(l[3])})
				off += 4
			}
			// Optional CST2 block: frames received beyond the checkpoint, as a
			// bit vector starting at checkpoint+2, least significant bit first.
			var received []int64
			if off+6 <= len(pkt) && string(pkt[off:off+4]) == "CST2" {
				n := int(pkt[off+5])
				vec := pkt[off+6 : min(len(pkt), off+6+n)]
				for i, b := range vec {
					for bit := range 8 {
						if b&(1<<bit) != 0 {
							received = append(received, int64(2+i*8+bit))
						}
					}
				}
			}
			s.onFeedback(pkt[16], losses, received)
		}
	}
}

// bitrateControl backs the encoder off while the receiver falls behind on
// acknowledging frames, and creeps back up once it keeps pace again.
type bitrateControl struct {
	kbps, min, max int
	set            func(kbps int)
	changed        time.Time
}

func newBitrate(kbps, min int, set func(int)) *bitrateControl {
	return &bitrateControl{kbps: kbps, min: min, max: kbps, set: set}
}

// update takes the age of the oldest unacknowledged frame.
func (b *bitrateControl) update(now time.Time, lag time.Duration) {
	since := now.Sub(b.changed)
	next := b.kbps
	switch {
	case lag > 400*time.Millisecond && since > 500*time.Millisecond:
		next = max(b.min, b.kbps*2/3)
	case lag < 150*time.Millisecond && since > 2*time.Second:
		next = min(b.max, b.kbps*11/10)
	}
	if next == b.kbps {
		return
	}
	log.Printf("bitrate %d -> %d kbit/s (oldest unacked frame %v old)", b.kbps, next, lag.Round(time.Millisecond))
	b.kbps, b.changed = next, now
	b.set(next)
}
