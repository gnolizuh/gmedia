package rtmp

import "io"

func (c *conn) readMessage() (error) {
	b, err := c.bufr.ReadByte()
	if err != nil {
		return err
	}

	/*
	 * Chunk stream IDs 2-63 can be encoded in
	 * the 1-byte version of this field.
	 *
	 *  0 1 2 3 4 5 6 7
	 * +-+-+-+-+-+-+-+-+
	 * |fmt|   cs id   |
	 * +-+-+-+-+-+-+-+-+
	 */
	fmt := uint8(b >> 6)
	csid := uint32(b & 0x3f)
	if csid == 0 {
		/*
		 * ID is computed as (the second byte + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     0     |  cs id - 64   |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		csid = 64
		b, err := c.bufr.ReadByte()
		if err != nil {
			return err
		}
		csid += uint8(b)
	} else if csid == 1 {
		/*
		 * ID is computed as ((the third byte)*256 +
		 * (the second byte) + 64).
		 *
		 *  0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 * |fmt|     1     |           cs id - 64          |
		 * +-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
		 */
		csid = 64
		b := make([]byte, 2)
		_, err := io.ReadFull(c.bufr, b)
		if err != nil {
			return err
		}
		csid += uint8(b[0])
		csid += 256 * uint8(b[1])
	}

	if fmt <= 2 {
		// pts := Read4Bytes()
		if fmt <= 1 {
			// mlen := Read4Bytes()
			if fmt == 0 {
				// msid := Read4Bytes()
			}
		}
	}

	return nil
}
