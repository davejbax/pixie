package grub

import (
	"debug/elf"
	"debug/pe"
	"errors"
	"fmt"
	"io"

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
}

// type elfSectionList []*elfSection

// func (l elfSectionList) GetByIndex(index int) (*elfSection, error) {
// 	for _, section := range l {
// 		if section.index == index {
// 			return section, nil
// 		}
// 	}

// 	return nil, errSectionNotFound
// }

type virtualSectionType int // TODO should this be = int?

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

func layoutVirtualSections(f *elf.File, headerSize uint64, alignment uint64) []*virtualSection {
	textSections := []*elfSection{}
	dataSections := []*elfSection{}
	bssSections := []*elfSection{}

	for sectionIndex, section := range f.Sections {
		hasExecInstr := section.Flags&elf.SHF_EXECINSTR > 0
		hasAlloc := section.Flags&elf.SHF_ALLOC > 0

		isection := &elfSection{Section: *section, index: sectionIndex}

		if hasExecInstr && hasAlloc {
			textSections = append(textSections, isection)
		} else if !hasExecInstr && hasAlloc {
			if section.Type == elf.SHT_NOBITS {
				bssSections = append(bssSections, isection)
			} else {
				dataSections = append(dataSections, isection)
			}
		} else {
			// TODO log ignored section here
		}
	}

	// Concat sections of the same type in a specific order: first .text, then
	// .data, then .bss
	addr := uint64(headerSize)

	virtualSections := make([]*virtualSection, 3)

	virtualSections[0], addr = createVirtualSection(addr, textSections, alignment, virtualSectionTypeText)
	virtualSections[1], addr = createVirtualSection(addr, dataSections, alignment, virtualSectionTypeData)
	virtualSections[2], addr = createVirtualSection(addr, bssSections, alignment, virtualSectionTypeBSS)

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
		}

		if virt.kind == virtualSectionTypeBSS && bssStart == 0 {
			bssStart = virt.offset
		}

		end = virt.offset + virt.size
	}

	relocatedSymbs := make([]elf.Symbol, 0, len(symbs))

	for _, symb := range symbs {
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
		} else if symb.Section == elf.SHN_ABS {
			// Symbols are absolute, and we have no need to relocate them
		} else {
			section, ok := sectionsByIndex[int(symb.Section)]
			if !ok {
				return nil, fmt.Errorf("could not find section with index '%d' defined by symbol '%s'", symb.Section, symb.Name)
			}

			symb.Value = section.addrInFile + symb.Value
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

func (s *virtualSection) Open() io.ReadCloser {
	return &virtualSectionReader{virt: s}
}

type virtualSectionReader struct {
	virt   *virtualSection
	index  int
	handle io.ReadSeeker
}

func (r *virtualSectionReader) Read(output []byte) (int, error) {
	for {
		if r.index >= len(r.virt.realSections) {
			return 0, io.EOF
		}

		if r.handle == nil {
			if r.virt.kind == virtualSectionTypeBSS {
				r.handle = &iometa.ZeroReader{Size: int(r.virt.realSections[r.index].Size)}
			} else {
				r.handle = r.virt.realSections[r.index].Open()
			}
		}

		read, err := r.handle.Read(output)
		eof := errors.Is(err, io.EOF)

		if eof {
			output = output[read:]
			r.index++
			r.handle = nil
			continue
		} else if err != nil {
			return read, fmt.Errorf("failed to read ELF section '%s': %w", r.virt.realSections[r.index].Name, err)
		}

		return read, nil
	}
}

func (r *virtualSectionReader) Close() error {
	return nil
}
