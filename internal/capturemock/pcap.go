package capturemock

import (
	"encoding/binary"
	"io"
)

// Classic libpcap ("pcap") file-format constants. This is the ubiquitous
// format Wireshark/tcpdump read; writing it here (a couple dozen bytes of
// header per file, encoding/binary only) avoids any third-party pcap
// dependency, per CLAUDE.md's stdlib-first rule.
const (
	pcapMagic        = 0xa1b2c3d4
	pcapVerMajor     = 2
	pcapVerMinor     = 4
	pcapSnaplen      = 65535
	linkTypeEthernet = 1
)

// pcapWriter writes a classic-pcap stream: one global header, then a record
// (16-byte header + frame bytes) per packet. Little-endian, matching the
// pcapMagic byte order.
type pcapWriter struct {
	w       io.Writer
	written int64
}

// newPcapWriter writes the global header to w and returns a writer for
// appending packet records.
func newPcapWriter(w io.Writer) (*pcapWriter, error) {
	var hdr [24]byte
	binary.LittleEndian.PutUint32(hdr[0:4], pcapMagic)
	binary.LittleEndian.PutUint16(hdr[4:6], pcapVerMajor)
	binary.LittleEndian.PutUint16(hdr[6:8], pcapVerMinor)
	binary.LittleEndian.PutUint32(hdr[8:12], 0)  // thiszone
	binary.LittleEndian.PutUint32(hdr[12:16], 0) // sigfigs
	binary.LittleEndian.PutUint32(hdr[16:20], pcapSnaplen)
	binary.LittleEndian.PutUint32(hdr[20:24], linkTypeEthernet)
	n, err := w.Write(hdr[:])
	pw := &pcapWriter{w: w, written: int64(n)}
	return pw, err
}

// writePacket appends one packet record with the given timestamp. It returns
// the total record size (header + frame) written.
func (p *pcapWriter) writePacket(tsSec, tsUsec uint32, frame []byte) (int, error) {
	var rec [16]byte
	binary.LittleEndian.PutUint32(rec[0:4], tsSec)
	binary.LittleEndian.PutUint32(rec[4:8], tsUsec)
	binary.LittleEndian.PutUint32(rec[8:12], uint32(len(frame)))
	binary.LittleEndian.PutUint32(rec[12:16], uint32(len(frame)))
	if _, err := p.w.Write(rec[:]); err != nil {
		return 0, err
	}
	if _, err := p.w.Write(frame); err != nil {
		return 0, err
	}
	total := len(rec) + len(frame)
	p.written += int64(total)
	return total, nil
}
