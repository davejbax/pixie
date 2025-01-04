package grub

import (
	"debug/elf"
	"debug/pe"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/davejbax/pixie/internal/align"
	"github.com/davejbax/pixie/internal/efipe"
	"github.com/davejbax/pixie/internal/iometa"
)

type elfSection struct {
	elf.Section

	// Index of the section as it appears in the ELF file
	index int

	// Address relative to the start of the image file
	addrInFile uint64

	relocationTypToFunc func(uint32) (f relocationFunc, ok bool)
	relocations         []*relocation
}

type virtualSectionType int

const (
	virtualSectionTypeText virtualSectionType = iota
	virtualSectionTypeData
	virtualSectionTypeBSS
	// TODO: modules section
	// TODO: reloc section
)

type virtualSection struct {
	offset       uint64
	size         uint64
	kind         virtualSectionType
	realSections []*elfSection
}

func layoutVirtualSections(f *elf.File, headerSize uint32, alignment uint32) []*virtualSection {
	textSections := []*elfSection{}
	dataSections := []*elfSection{}
	bssSections := []*elfSection{}

	for sectionIndex, section := range f.Sections {
		hasExecInstr := section.Flags&elf.SHF_EXECINSTR > 0
		hasAlloc := section.Flags&elf.SHF_ALLOC > 0

		isection := &elfSection{Section: *section, index: sectionIndex}

		switch {
		case hasExecInstr && hasAlloc:
			textSections = append(textSections, isection)
		case !hasExecInstr && hasAlloc:
			if section.Type == elf.SHT_NOBITS {
				bssSections = append(bssSections, isection)
			} else {
				dataSections = append(dataSections, isection)
			}
		default:
			slog.Warn("excluding section (not text/data/BSS)",
				"section", section.Name,
			)
		}
	}

	// Concat sections of the same type in a specific order: first .text, then
	// .data, then .bss (which is also placed in the virtual .data section, following GRUB behaviour)
	addr := uint64(headerSize)
	dataSections = append(dataSections, bssSections...)

	virtualSections := make([]*virtualSection, 2)
	virtualSections[0], addr = createVirtualSection(addr, textSections, uint64(alignment), virtualSectionTypeText)
	virtualSections[1], addr = createVirtualSection(addr, dataSections, uint64(alignment), virtualSectionTypeData) //nolint:ineffassign,staticcheck

	return virtualSections
}

func createVirtualSection(addr uint64, sourceSections []*elfSection, alignment uint64, kind virtualSectionType) (*virtualSection, uint64) {
	addr = align.Address(addr, alignment)
	virt := &virtualSection{kind: kind, offset: addr}
	relocatedSections := make([]*elfSection, 0, len(sourceSections))

	for _, section := range sourceSections {
		if section.Addralign > 0 {
			addr = align.Address(addr, section.Addralign)
		}

		sectionWithAddr := *section
		sectionWithAddr.addrInFile = addr
		relocatedSections = append(relocatedSections, &sectionWithAddr)

		slog.Debug("locating ELF section",
			"section", sectionWithAddr.Name,
			"addr", fmt.Sprintf("0x%02x", sectionWithAddr.addrInFile),
		)

		addr += section.Size
	}

	// Align the end of the section to the given alignment as well
	addr = align.Address(addr, alignment)

	virt.size = addr - virt.offset
	virt.realSections = relocatedSections

	return virt, addr
}

var errBSSSymbolButNoBSSSection = errors.New("BSS symbol found but no BSS virtual section created")

// Create a new list of symbols where the symbols' values are relative to the start of the
// image file we're producing, and also take into account the new section addresses
// TODO: surely we want a []*elf.Symbol here?
func relocateSymbols(f *elf.File, virtualSections []*virtualSection) ([]elf.Symbol, error) {
	symbs, err := f.Symbols()
	if err != nil {
		return nil, fmt.Errorf("failed to get symbols in file: %w", err)
	}

	// It's probably not technically correct to use zero as nil here, but
	// I think the odds of the BSS start being at zero are nonexistent: we'll
	// always have a header in the file, and we'll almost certainly have .text
	// first
	bssStart := uint64(0)
	end := uint64(0)

	sectionsByIndex := make(map[int]*elfSection)
	for _, virt := range virtualSections {
		for _, section := range virt.realSections {
			sectionsByIndex[section.index] = section

			if section.Type == elf.SHT_NOBITS && bssStart == 0 {
				bssStart = section.addrInFile
			}
		}

		end = virt.offset + virt.size
	}

	relocatedSymbs := make([]elf.Symbol, 0, len(symbs))

	// Add in the undefined symbol: [elf.File.Symbols()] omits this!
	relocatedSymbs = append(relocatedSymbs, elf.Symbol{})

	for i, symb := range symbs {
		if symb.Section == elf.SHN_UNDEF {
			if symb.Name == symbBssStart {
				// Ensure we do actually have a BSS section!
				if bssStart == 0 {
					return nil, errBSSSymbolButNoBSSSection
				}

				symb.Value = bssStart
			} else if symb.Name == symbEnd {
				symb.Value = end
			} else {
				return nil, fmt.Errorf("error processing symbol '%s': %w", symb.Name, errUnrecognizedSymbol)
			}
		} else if symb.Section == elf.SHN_ABS { //nolint:revive
			// Symbols are absolute, and we have no need to relocate them
		} else {
			section, ok := sectionsByIndex[int(symb.Section)]
			if !ok {
				return nil, fmt.Errorf("could not find section with index '%d' defined by symbol '%s'", symb.Section, symb.Name)
			}

			oldValue := symb.Value
			symb.Value = section.addrInFile + symb.Value
			slog.Debug("relocating symbol",
				"symbol", symb.Name,
				"index", i+1, // symbs here starts at 1 index, due to the [elf] package
				"from", fmt.Sprintf("0x%02x", oldValue),
				"to", fmt.Sprintf("0x%02x", symb.Value),
				"section", section.Name,
			)
		}

		relocatedSymbs = append(relocatedSymbs, symb)
	}

	return relocatedSymbs, nil
}

func (t virtualSectionType) Name() string {
	switch t {
	case virtualSectionTypeText:
		return efipe.SectionText
	case virtualSectionTypeData:
		return efipe.SectionData
	case virtualSectionTypeBSS:
		return efipe.SectionBSS
	default:
		panic("invalid virtual section type")
	}
}

func (t virtualSectionType) Characteristics() uint32 {
	switch t {
	case virtualSectionTypeText:
		return pe.IMAGE_SCN_CNT_CODE | pe.IMAGE_SCN_MEM_EXECUTE | pe.IMAGE_SCN_MEM_READ
	case virtualSectionTypeData:
		return pe.IMAGE_SCN_CNT_INITIALIZED_DATA | pe.IMAGE_SCN_MEM_READ | pe.IMAGE_SCN_MEM_WRITE
	case virtualSectionTypeBSS:
		return pe.IMAGE_SCN_CNT_UNINITIALIZED_DATA | pe.IMAGE_SCN_MEM_READ | pe.IMAGE_SCN_MEM_WRITE
	default:
		panic("invalid virtual section type")
	}
}

func (s *virtualSection) Header() pe.SectionHeader {
	return pe.SectionHeader{
		Name:           s.kind.Name(),
		VirtualSize:    uint32(s.size),
		VirtualAddress: uint32(s.offset),
		Size:           uint32(s.size),
		Offset:         uint32(s.offset),

		// Always set to zero for executables
		PointerToRelocations: 0,

		// Always set to zero, as COFF debugging information is deprecated
		PointerToLineNumbers: 0,

		NumberOfRelocations: 0,
		NumberOfLineNumbers: 0,
		Characteristics:     s.kind.Characteristics(),
	}
}

func (s *virtualSection) WriteTo(w io.Writer) (int64, error) {
	cw := &iometa.CountingWriter{Writer: w}

	initialAddr := s.realSections[0].addrInFile

	for _, section := range s.realSections {
		// If there's padding before the start of this section, write it now
		if uint64(cw.BytesWritten()) < section.addrInFile-initialAddr {
			if err := iometa.WriteZeros(cw, int(section.addrInFile-initialAddr)-cw.BytesWritten()); err != nil {
				return int64(cw.BytesWritten()), fmt.Errorf("failed to write pre-padding for underlying ELF section '%s': %w", section.Name, err)
			}
		}

		var sectionData io.Reader

		if section.Type == elf.SHT_NOBITS {
			sectionData = &iometa.ZeroReader{Size: int(section.Size)}
		} else {
			// If we have relocations, do them now. This will (as is necessitated
			// by the nature of doing these relocations) read the entire section
			// into memory.
			if len(section.relocations) > 0 {
				var err error
				sectionData, err = newRelocationReader(section)
				if err != nil {
					return int64(cw.BytesWritten()), fmt.Errorf("failed to apply relocations to section: %w", err)
				}
			} else {
				// If no relocations, we can read directly from the section
				sectionData = section.Open()
			}
		}

		_, err := io.Copy(cw, sectionData)
		if err != nil {
			return int64(cw.BytesWritten()), fmt.Errorf("failed to write data from section '%s': %w", section.Name, err)
		}
	}

	return int64(cw.BytesWritten()), nil
}
