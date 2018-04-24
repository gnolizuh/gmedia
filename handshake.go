package rtmp

import (
	"crypto/hmac"
	"crypto/sha256"
	"bytes"
	"math/rand"
	"encoding/binary"
	"io"
	"log"
	"errors"
	"fmt"
)

/* RTMP handshake :
 *
 *          =peer1=                      =peer2=
 * challenge ----> (.....[digest1]......) ----> 1537 bytes
 * response  <---- (...........[digest2]) <---- 1536 bytes
 *
 *
 * - both packets contain random bytes except for digests
 * - digest1 position is calculated on random packet bytes
 * - digest2 is always at the end of the packet
 *
 * digest1: HMAC_SHA256(packet, peer1_partial_key)
 * digest2: HMAC_SHA256(packet, HMAC_SHA256(digest1, peer2_full_key))
 */

/* Handshake keys */
var (
	ServerKey = []byte {
		'G', 'e', 'n', 'u', 'i', 'n', 'e', ' ', 'A', 'd', 'o', 'b', 'e', ' ',
		'F', 'l', 'a', 's', 'h', ' ', 'M', 'e', 'd', 'i', 'a', ' ',
		'S', 'e', 'r', 'v', 'e', 'r', ' ',
		'0', '0', '1',

		0xF0, 0xEE, 0xC2, 0x4A, 0x80, 0x68, 0xBE, 0xE8, 0x2E, 0x00, 0xD0, 0xD1,
		0x02, 0x9E, 0x7E, 0x57, 0x6E, 0xEC, 0x5D, 0x2D, 0x29, 0x80, 0x6F, 0xAB,
		0x93, 0xB8, 0xE6, 0x36, 0xCF, 0xEB, 0x31, 0xAE,
	}

	ServerFullKey = ServerKey
	ServerPartialKey = ServerKey[:36]

	ClientKey = []byte {
		'G', 'e', 'n', 'u', 'i', 'n', 'e', ' ', 'A', 'd', 'o', 'b', 'e', ' ',
		'F', 'l', 'a', 's', 'h', ' ', 'P', 'l', 'a', 'y', 'e', 'r', ' ',
		'0', '0', '1',

		0xF0, 0xEE, 0xC2, 0x4A, 0x80, 0x68, 0xBE, 0xE8, 0x2E, 0x00, 0xD0, 0xD1,
		0x02, 0x9E, 0x7E, 0x57, 0x6E, 0xEC, 0x5D, 0x2D, 0x29, 0x80, 0x6F, 0xAB,
		0x93, 0xB8, 0xE6, 0x36, 0xCF, 0xEB, 0x31, 0xAE,
	}

	ClientFullKey = ClientKey
	ClientPartialKey = ClientKey[:30]

	ServerVersion = []byte {
		0x0D, 0x0E, 0x0A, 0x0D,
	}

	ClientVersion = []byte {
		0x0C, 0x00, 0x0D, 0x0E,
	}

	ProtoVersion = byte('\x03')

	HandshakeKeyLen = uint32(32)
	HandshakeChallengeSize = uint32(1537)
	HandshakeResponseSize = HandshakeChallengeSize - 1
)

const (
	StateServerRecvChallenge connState = iota
	StateServerSendChallenge
	StateServerRecvResponse
	StateServerSendResponse
	StateServerDone
)

func makeDigest(b, key []byte, offs uint32) ([]byte, error) {

	h := hmac.New(sha256.New, key)
	if offs > 0 {
		if _, err := h.Write(b[:offs]); err != nil {
			return nil, err
		}
		if _, err := h.Write(b[offs + HandshakeKeyLen:]); err != nil {
			return nil, err
		}
	} else {
		if _, err := h.Write(b); err != nil {
			return nil, err
		}
	}

	return h.Sum(nil), nil
}

func findDigest(b, key []byte, base uint32) (bool, uint32) {
	offs := uint32(0)
	for n := uint32(0); n < 4; n++ {
		offs += uint32(b[base + n])
	}
	offs = (offs % 728) + base + 4

	hs, err := makeDigest(b, key, offs)
	if err != nil {
		return false, 0
	}

	return bytes.Equal(b[offs:], hs), offs
}

func writeDigest(b, key []byte, base uint32) error {
	offs := uint32(0)
	for n := uint32(8); n < 12; n++ {
		offs += uint32(b[base + n])
	}
	offs = (offs % 728) + base + 12;

	hs, err := makeDigest(b, key, offs)
	if err != nil {
		return err
	}

	for n, h := range hs {
		b[offs + uint32(n)] = h
	}

	return nil
}

func makeRandom(p []byte) {
	for n := 0; n < len(p); n++ {
		p[n] = byte(rand.Int())
	}
}

func (c *conn) handshake() error {
	var err error
	for c.state != StateServerDone {
		switch c.state {
		case StateServerRecvChallenge:
			err = c.recvChallenge(ClientPartialKey, ServerFullKey)
		case StateServerSendChallenge:
			err = c.sendChallenge(ServerVersion, ServerPartialKey)
		case StateServerRecvResponse:
			err = c.recvResponse()
		case StateServerSendResponse:
			err = c.sendResponse()
		}

		if err != nil {
			return err
		}

		c.state ++
	}

	return nil
}

// send S0 + S1
func (c *conn) sendChallenge(version, key []byte) error {
	s01 := make([]byte, HandshakeChallengeSize)

	// s0, version MUST be 0x03
	s01[0] = ProtoVersion

	// s1
	binary.BigEndian.PutUint32(s01[1:5], c.lepoch) // timestamp
	copy(s01[5:9], version)                        // version(zero)

	makeRandom(s01[9:])                            // random
	if err := writeDigest(s01[1:], key, 0); err != nil {
		return err
	}

	c.bufw.Write(s01)

	return nil
}

// recv C0 + C1
func (c *conn) recvChallenge(ckey, skey []byte) error {
	c01 := make([]byte, HandshakeChallengeSize)
	if _, err := io.ReadFull(c.bufr, c01); err != nil {
		return err
	}

	// c0, version MUST be 0x03
	if c01[0] != ProtoVersion {
		return errors.New(fmt.Sprintf("handshake: unexpected RTMP version: %d", int(c01[0])))
	}

	// c1
	c.repoch = binary.BigEndian.Uint32(c01[1:5]) // timestamp

	log.Printf("handshake: peer version=%d.%d.%d.%d epoch=%d", c01[8], c01[7], c01[6], c01[5], c.repoch)

	if uint32(c01[5:9]) == 0 {
		return nil
	}

	find, offs := findDigest(c01[1:], ckey, 772)
	if !find {
		find, offs = findDigest(c01[1:], ckey, 8)
	}

	if !find {
		return errors.New("handshake: digest not found")
	}

	var err error
	c.digest, err = makeDigest(c01[1 + offs:], skey, offs)
	if err != nil {
		return err
	}

	return nil
}

// send S2
func (c *conn) sendResponse() error {
	s2 := make([]byte, HandshakeResponseSize)

	// s2
	makeRandom(s2)

	offs := HandshakeResponseSize - HandshakeKeyLen
	hs, err := makeDigest(s2, c.digest, offs)
	if err != nil {
		return err
	}

	for n, h := range hs {
		s2[offs + uint32(n)] = h
	}

	c.bufw.Write(s2)

	return nil
}

// recv C2
func (c *conn) recvResponse() error {
	// c2
	c2 := make([]byte, HandshakeResponseSize)
	if _, err := io.ReadFull(c.bufr, c2); err != nil {
		return err
	}

	return nil
}
