package efipe

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/davejbax/pixie/internal/iometa"
	"github.com/lunixbochs/struc"
)

var dosMagic []byte = []byte{0x4D, 0x5A}

// x86 real mode instructions for MS-DOS, printing out a message that the program
// cannot be run in DOS mode
var dosStub []byte = []byte{
	0x0E, 0x1F, 0xBA, 0x0E, 0x00, 0xB4, 0x09, 0xCD, 0x21, 0xB8, 0x01, 0x4C, 0xCD, 0x21, 0x54, 0x68,
	0x69, 0x73, 0x20, 0x70, 0x72, 0x6F, 0x67, 0x72, 0x61, 0x6D, 0x20, 0x63, 0x61, 0x6E, 0x6E, 0x6F,
	0x74, 0x20, 0x62, 0x65, 0x20, 0x72, 0x75, 0x6E, 0x20, 0x69, 0x6E, 0x20, 0x44, 0x4F, 0x53, 0x20,
	0x6D, 0x6F, 0x64, 0x65, 0x2E, 0x0D, 0x0D, 0x0A, 0x24, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00,
}

const dosHeaderSizeBytes = 64
const pageSizeBytes = 512
const paragraphSizeBytes = 16

type dosHeader struct {
	BytesOnLastPage      uint16
	PagesInFile          uint16
	RelocationItems      uint16
	HeaderSizeParagraphs uint16
	MinimumAllocation    uint16
	MaximumAllocation    uint16
	InitialSS            uint16
	InitialSP            uint16
	Checksum             uint16
	InitialIP            uint16
	InitialCS            uint16
	RelocationTableAddr  uint16
	OverlayNumber        uint16
	Reserved1            uint64
	OEMIdentifier        uint16
	OEMInfo              uint16
	Reserved2            []byte `struc:"[20]uint8"`
	PEHeaderStartAddr    uint32
}

func (d *dosHeader) MarshalBinary() ([]byte, error) {
	var buff bytes.Buffer

	if _, err := d.WriteTo(&buff); err != nil {
		return nil, fmt.Errorf("failed to write DOS header to buffer: %w", err)
	}

	return buff.Bytes(), nil
}

func (d *dosHeader) WriteTo(output io.Writer) (int64, error) {
	countedOutput := &iometa.CountingWriter{Writer: output}

	if written, err := countedOutput.Write(dosMagic); err != nil {
		return int64(written), fmt.Errorf("failed to write DOS magic: %w", err)
	}

	if err := struc.PackWithOptions(countedOutput, d, &struc.Options{
		Order: binary.LittleEndian,
	}); err != nil {
		return int64(countedOutput.BytesWritten()), fmt.Errorf("failed to write DOS header: %w", err)
	}

	return int64(countedOutput.BytesWritten()), nil
}

type dosImage struct {
	header  *dosHeader
	program []byte
}

func newDOSImage(program []byte, peStartAddr uint32) *dosImage {
	numPages := int(math.Ceil(float64(len(program)) / float64(pageSizeBytes)))
	lastPageSize := numPages*pageSizeBytes - len(program)

	header := &dosHeader{
		BytesOnLastPage:      uint16(lastPageSize),
		PagesInFile:          uint16(numPages),
		RelocationItems:      0,
		HeaderSizeParagraphs: 4, // our DOS header is always 64 bytes

		// Most things tend to 'require' 10 paragraphs, and 'request'
		// the maximum number (0xFFFF). I'm not sure where these come
		// from, so just assume there's a good reason for this.
		MinimumAllocation: 10,
		MaximumAllocation: 65535,

		InitialSS: 0,
		InitialSP: 256, // Fairly arbitrary amount of stack space chosen here

		Checksum: 0, // Generally unused, so be lazy and set it to zero

		InitialIP: 0,
		InitialCS: 0,

		// Say relocation table is after this header
		// It has zero items, so theoretically shouldn't matter
		RelocationTableAddr: dosHeaderSizeBytes,

		OverlayNumber: 0, // This is the main executable
	}

	// Align to the closest boundary that's divisible by programAlignment
	// Pad with zeros until this point
	header.PEHeaderStartAddr = peStartAddr

	return &dosImage{
		header:  header,
		program: program,
	}
}

func (d *dosImage) WriteTo(output io.Writer) (int64, error) {
	written, err := d.header.WriteTo(output)
	if err != nil {
		return written, fmt.Errorf("failed to write DOS image header: %w", err)
	}

	progWritten, err := output.Write(d.program)
	written += int64(progWritten)
	if err != nil {
		return written, fmt.Errorf("failed to write DOS program: %w", err)
	}

	padding := int(int64(d.header.PEHeaderStartAddr) - written)
	if padding > 0 {
		paddingWritten, err := output.Write(make([]byte, padding))
		written += int64(paddingWritten)
		if err != nil {
			return written, fmt.Errorf("failed to write DOS padding: %w", err)
		}
	}

	if written != int64(d.header.PEHeaderStartAddr) {
		panic(fmt.Sprintf("wrote an invalid number of bytes to DOS image: wrote %d, expected %d", written, d.header.PEHeaderStartAddr))
	}

	return written, nil
}
