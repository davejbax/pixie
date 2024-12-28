package efipe

import (
	"bytes"
	"debug/pe"
	"encoding/binary"
	"fmt"
	"io"
	"log/slog"
	"slices"

	"github.com/davejbax/pixie/internal/align"
	"github.com/davejbax/pixie/internal/iometa"
	"github.com/lunixbochs/struc"
)

type RelocationType = int

// Relocation types
const (
	ImageRelBasedAbsolute          RelocationType = 0
	ImageRelBasedHigh              RelocationType = 1
	ImageRelBasedLow               RelocationType = 2
	ImageRelBasedHighLow           RelocationType = 3
	ImageRelBasedHighAdj           RelocationType = 4
	ImageRelBasedMipsJmpAddr       RelocationType = 5
	ImageRelBasedArmMov32          RelocationType = 5
	ImageRelBasedRiscVHigh20       RelocationType = 5
	ImageRelBasedThumbMov32        RelocationType = 7
	ImageRelBasedRiscVLow12I       RelocationType = 7
	ImageRelBasedLoongArch32MarkLA RelocationType = 8
	ImageRelBasedLoongArch64MarkLA RelocationType = 8
	ImageRelBasedMipsJmpAddr16     RelocationType = 9
	ImageRelBasedDir64             RelocationType = 10
)

type Relocation struct {
	Kind       RelocationType
	FileOffset uint64
}

type relocationBlock struct {
	pageRVA   uint32
	blockSize uint32
	entries   []uint16
}

func (b *relocationBlock) WriteTo(w io.Writer) (int64, error) {
	cw := &iometa.CountingWriter{Writer: w}
	opts := &struc.Options{Order: binary.LittleEndian}

	if err := struc.PackWithOptions(cw, b.pageRVA, opts); err != nil {
		return int64(cw.BytesWritten()), err
	}

	if err := struc.PackWithOptions(cw, b.blockSize, opts); err != nil {
		return int64(cw.BytesWritten()), err
	}

	for _, entry := range b.entries {
		if err := struc.PackWithOptions(cw, entry, opts); err != nil {
			return int64(cw.BytesWritten()), err
		}
	}

	if cw.BytesWritten() < int(b.blockSize) {
		if err := iometa.WriteZeros(cw, int(b.blockSize)-cw.BytesWritten()); err != nil {
			return int64(cw.BytesWritten()), fmt.Errorf("failed to write relocation block padding: %w", err)
		}
	}

	return int64(cw.BytesWritten()), nil
}

type relocationSection struct {
	data   []byte
	offset uint32
}

var _ Section = &relocationSection{}

func newRelocationSection(relocs []*Relocation, offset uint32) *relocationSection {
	relocsByPageRVA := make(map[uint32][]*Relocation)

	// Bucket relocations by their (4k) page. Each of these will become a relocation block
	for _, reloc := range relocs {
		pageRVA := uint32(reloc.FileOffset & ^(uint64(UEFIPageSize) - 1))
		relocsByPageRVA[pageRVA] = append(relocsByPageRVA[pageRVA], reloc)
	}

	blocks := make([]*relocationBlock, 0, len(relocsByPageRVA))
	totalSize := uint32(0)

	// Create an actual relocation block structure for each bucket. This is what
	// will end up in the final PE section.
	for pageRVA, blockRelocs := range relocsByPageRVA {
		entries := make([]uint16, 0, len(blockRelocs))
		for _, reloc := range blockRelocs {
			entries = append(entries,
				// Relocation type is stored in upper 4 bits; offset from page RVA is
				// stored in lower 12 bits
				uint16((reloc.FileOffset-uint64(pageRVA))&0x0FFF)|uint16(reloc.Kind<<12),
			)
		}

		if len(entries)*2+8 > UEFIPageSize {
			// Sanity check; theoretically this should never happen!
			panic(fmt.Sprintf("too many entries in relocation block: have %d, which would exceed UEFI page size (%d)", len(entries), UEFIPageSize))
		}

		// Blocks must be aligned to 32 bit boundaries
		blockSize := align.Address(uint32(len(entries)*2+8), 4)
		blocks = append(blocks, &relocationBlock{
			pageRVA: pageRVA,

			// blockSize is the full block size, including the pageRVA and blockSize.
			// Each entry is 2 bytes, pageRVA is 4 bytes, blockSize is 4 bytes
			blockSize: blockSize,
			entries:   entries,
		})
		totalSize += blockSize
	}

	// Not sure if this is necessary, but seems like it might be good practice to
	// order our relocation section based on the page RVA (in ascending order)
	slices.SortFunc(blocks, func(a, b *relocationBlock) int {
		return int(a.pageRVA) - int(b.pageRVA)
	})

	alignedTotalSize := align.Address(offset+totalSize, UEFIPageSize) - offset
	if endPadding := alignedTotalSize - totalSize; endPadding > 0 {
		blocks[len(blocks)-1].blockSize += endPadding
	}

	data := bytes.NewBuffer(make([]byte, 0, totalSize))
	for i, block := range blocks {
		written, err := block.WriteTo(data)
		if err != nil {
			// TODO proper error handling here
			panic(err)
		}

		if written != int64(block.blockSize) {
			panic(fmt.Sprintf("PE relocation block had unexpected number of bytes written: expected %d, got %d", block.blockSize, written))
		}

		slog.Debug("created PE relocation block",
			"index", i,
			"pageRVA", block.pageRVA,
			"blockSize", block.blockSize,
			"entries", len(block.entries),
		)
	}

	return &relocationSection{
		data:   data.Bytes(),
		offset: offset,
	}
}

func (s *relocationSection) Header() pe.SectionHeader {
	end := align.Address(s.offset+uint32(len(s.data)), UEFIPageSize)
	return pe.SectionHeader{
		Name:           sectionReloc,
		VirtualSize:    end - s.offset,
		VirtualAddress: s.offset,
		Size:           end - s.offset,
		Offset:         s.offset,

		// These fields are all unused for executables or otherwise deprecated
		PointerToRelocations: 0,
		PointerToLineNumbers: 0,
		NumberOfRelocations:  0,
		NumberOfLineNumbers:  0,

		Characteristics: pe.IMAGE_SCN_CNT_INITIALIZED_DATA | pe.IMAGE_SCN_MEM_DISCARDABLE | pe.IMAGE_SCN_MEM_READ,
	}
}

func (s *relocationSection) Open() io.ReadCloser {
	return &iometa.Closifier{Reader: bytes.NewReader(s.data)}
}
