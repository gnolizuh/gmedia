package rtmp

import (
	"crypto/hmac"
	"crypto/sha256"
	"bytes"
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
const (
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

	HandshakeKeyLen = 32
	HandshakeBufSize = 1537
)

func makeDigest(b []byte, key string, offs uint32) ([]byte, error) {

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

func findDigest(b []byte, key string, base uint32) (bool, error) {
	offs := uint32(0)
	for n := uint32(0); n < 4; n++ {
		offs += uint32(b[base + n])
	}
	offs = (offs % 728) + base + 4

	dig, err := makeDigest(b, key, offs)
	if err != nil {
		return false, err
	}

	return bytes.Equal(b[offs:], dig), nil
}

func writeDigest(b []byte, key string, base uint32) ([]byte, error) {
	offs := uint32(0)
	for n := uint32(8); n < 12; n++ {
		offs += uint32(b[base + n])
	}
	offs = (offs % 728) + base + 12;

	dig, err := makeDigest(b, key, offs)
	if err != nil {
		return false, err
	}

	return dig, nil
}

func handshake(c *conn) error {
	return nil
}
