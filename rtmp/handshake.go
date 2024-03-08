//
// Copyright [2024] [https://github.com/gnolizuh]
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//

package rtmp

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"math/rand"
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
	ServerKey = []byte{
		'G', 'e', 'n', 'u', 'i', 'n', 'e', ' ', 'A', 'd', 'o', 'b', 'e', ' ',
		'F', 'l', 'a', 's', 'h', ' ', 'M', 'e', 'd', 'i', 'a', ' ',
		'S', 'e', 'r', 'v', 'e', 'r', ' ',
		'0', '0', '1',

		0xF0, 0xEE, 0xC2, 0x4A, 0x80, 0x68, 0xBE, 0xE8, 0x2E, 0x00, 0xD0, 0xD1,
		0x02, 0x9E, 0x7E, 0x57, 0x6E, 0xEC, 0x5D, 0x2D, 0x29, 0x80, 0x6F, 0xAB,
		0x93, 0xB8, 0xE6, 0x36, 0xCF, 0xEB, 0x31, 0xAE,
	}

	ServerFullKey    = ServerKey
	ServerPartialKey = ServerKey[:36]

	ClientKey = []byte{
		'G', 'e', 'n', 'u', 'i', 'n', 'e', ' ', 'A', 'd', 'o', 'b', 'e', ' ',
		'F', 'l', 'a', 's', 'h', ' ', 'P', 'l', 'a', 'y', 'e', 'r', ' ',
		'0', '0', '1',

		0xF0, 0xEE, 0xC2, 0x4A, 0x80, 0x68, 0xBE, 0xE8, 0x2E, 0x00, 0xD0, 0xD1,
		0x02, 0x9E, 0x7E, 0x57, 0x6E, 0xEC, 0x5D, 0x2D, 0x29, 0x80, 0x6F, 0xAB,
		0x93, 0xB8, 0xE6, 0x36, 0xCF, 0xEB, 0x31, 0xAE,
	}

	ClientFullKey    = ClientKey
	ClientPartialKey = ClientKey[:30]

	ServerVersion = []byte{0x0D, 0x0E, 0x0A, 0x0D}
	ClientVersion = []byte{0x0C, 0x00, 0x0D, 0x0E}

	ProtoVersion = byte('\x03')

	HandshakeKeyLen        = uint32(32)
	HandshakeChallengeSize = uint32(1537)
	HandshakeResponseSize  = HandshakeChallengeSize - 1
)

type ConnState uint

const (
	StateServerRecvChallenge ConnState = iota
	StateServerSendChallenge
	StateServerSendResponse
	StateServerRecvResponse
	StateServerDone

	StateClientSendChallenge
	StateClientRecvChallenge
	StateClientSendResponse
	StateClientRecvResponse
	StataClientDone

	StateServerNew = StateServerRecvChallenge
	StateClientNew = StateClientSendChallenge
)

func makeDigest(b, key []byte, s uint32) ([]byte, error) {
	h := hmac.New(sha256.New, key)
	if s > 0 {
		if _, err := h.Write(b[:s]); err != nil {
			return nil, err
		}
	}
	if s+HandshakeKeyLen < uint32(len(b)) {
		if _, err := h.Write(b[s+HandshakeKeyLen:]); err != nil {
			return nil, err
		}
	}
	return h.Sum(nil), nil
}

func makeDigestWhole(b, key []byte) ([]byte, error) {
	h := hmac.New(sha256.New, key)
	if _, err := h.Write(b); err != nil {
		return nil, err
	}
	return h.Sum(nil), nil
}

func findDigest(b, key []byte, base uint32) (bool, uint32) {
	s := uint32(0)
	for n := uint32(0); n < 4; n++ {
		s += uint32(b[base+n])
	}
	s = (s % 728) + base + 4
	hs, err := makeDigest(b, key, s)
	if err != nil {
		return false, 0
	}
	return bytes.Equal(b[s:s+uint32(len(hs))], hs), s
}

func writeDigest(b, key []byte, base uint32) error {
	s := uint32(0)
	for n := uint32(0); n < 4; n++ {
		s += uint32(b[base+n])
	}
	s = (s % 728) + base + 4
	hs, err := makeDigest(b, key, s)
	if err != nil {
		return err
	}
	copy(b[s:], hs)
	return nil
}

func makeRandom(p []byte) {
	for n := 0; n < len(p); n++ {
		p[n] = byte(rand.Int())
	}
}

func (conn *conn) handshake() error {
	var err error
	for {
		state := ConnState(conn.state.Load())
		switch state {
		case StateServerRecvChallenge:
			err = conn.recvChallenge(ClientPartialKey, ServerFullKey)
		case StateServerSendChallenge:
			err = conn.sendChallenge(ServerVersion, ServerPartialKey)
		case StateServerSendResponse:
			err = conn.sendResponse()
		case StateServerRecvResponse:
			err = conn.recvResponse()
		case StateServerDone:
			return nil
		case StateClientSendChallenge:
			err = conn.sendChallenge(ClientVersion, ClientPartialKey)
		case StateClientRecvChallenge:
			err = conn.recvChallenge(ServerPartialKey, ClientFullKey)
		case StateClientSendResponse:
			err = conn.sendResponse()
		case StateClientRecvResponse:
			err = conn.recvResponse()
		case StataClientDone:
			return nil
		default:
			panic("unhandled default case")
		}

		if err != nil {
			return err
		}

		conn.setState(conn.rwc, state+1)
	}

	return nil
}

// sendChallenge send S0 + S1
func (conn *conn) sendChallenge(version, peerKey []byte) error {
	s01 := make([]byte, HandshakeChallengeSize)

	// s0, version MUST be 0x03
	s01[0] = ProtoVersion

	// s1
	binary.BigEndian.PutUint32(s01[1:5], conn.epoch) // timestamp
	copy(s01[5:9], version)                          // version(zero)

	makeRandom(s01[9:]) // random
	err := writeDigest(s01[1:], peerKey, 8)
	if err != nil {
		return err
	}

	_, err = conn.bufw.Write(s01)
	if err != nil {
		return err
	}

	return conn.bufw.Flush()
}

// recvChallenge recv C0 + C1
func (conn *conn) recvChallenge(peerKey, key []byte) error {
	c01 := make([]byte, HandshakeChallengeSize)
	if _, err := conn.ReadFull(c01); err != nil {
		return err
	}

	// c0, version MUST be 0x03
	if c01[0] != ProtoVersion {
		return errors.New(fmt.Sprintf("handshake: unexpected RTMP version: %d", int(c01[0])))
	}

	// c1
	conn.incomingEpoch = binary.BigEndian.Uint32(c01[1:5]) // timestamp

	log.Printf("handshake: peer version=%d.%d.%d.%d epoch=%d", c01[8], c01[7], c01[6], c01[5], conn.incomingEpoch)

	if binary.BigEndian.Uint32(c01[5:9]) == 0 {
		return nil
	}

	find, s := findDigest(c01[1:], peerKey, 772)
	if !find {
		find, s = findDigest(c01[1:], peerKey, 8)
	}
	if !find {
		return errors.New("handshake: digest not found")
	}

	var err error
	conn.digest, err = makeDigestWhole(c01[1+s:1+s+HandshakeKeyLen], key)
	if err != nil {
		return err
	}

	return nil
}

// sendResponse send S2
func (conn *conn) sendResponse() error {
	s2 := make([]byte, HandshakeResponseSize)

	// s2
	makeRandom(s2)
	s := HandshakeResponseSize - HandshakeKeyLen
	hs, err := makeDigest(s2, conn.digest, s)
	if err != nil {
		return err
	}
	copy(s2[s:], hs)

	_, err = conn.bufw.Write(s2)
	if err != nil {
		return err
	}

	return conn.bufw.Flush()
}

// recvResponse recv C2
func (conn *conn) recvResponse() error {
	// c2
	c2 := make([]byte, HandshakeResponseSize)
	if _, err := conn.ReadFull(c2); err != nil {
		return err
	}

	return nil
}
