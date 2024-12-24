package grub

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"
)

const (
	// Name of unreferenced BSS start symbol
	symbBssStart = "__bss_start"

	// Name of special unreferenced 'end' symbol
	symbEnd = "end"
)

type imageSection struct {
	elf.Section

	// Index of the section as it appears in the ELF file
	index int

	// Address relative to the start of the image file
	addrInFile uint64
}

type imageSectionList []*imageSection

type image struct {
	headerSize uint64
	sections   imageSectionList
	size       uint64
	symbols    []elf.Symbol
}

var (
	errSectionNotFound    = errors.New("section with given index not found")
	errUnrecognizedSymbol = errors.New("unrecognised symbol defined relative to SHN_UNDEF")
)

func (l imageSectionList) GetByIndex(index int) (*imageSection, error) {
	for _, section := range l {
		if section.index == index {
			return section, nil
		}
	}

	return nil, errSectionNotFound
}

func NewImage(r io.ReaderAt, headerSize uint64, alignment uint64) (*image, error) {
	elfFile, err := elf.NewFile(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read ELF file: %w", err)
	}

	if elfFile.Machine != elf.EM_X86_64 {
		panic("ELF file is not x86_64; not implemented other machine types yet")
	}

	sections, bssStart, sectionsEnd := placeSections(elfFile, headerSize)

	sectionsEnd = align(sectionsEnd, alignment)

	symbs, err := relocateSymbols(elfFile, sections, bssStart, sectionsEnd)
	if err != nil {
		return nil, fmt.Errorf("failed to relocate symbols: %w", err)
	}

	// TODO: Implement instruction relocation
	for _, section := range sections {
		if section.Type == elf.SHT_REL || section.Type == elf.SHT_RELA {
			panic("relocation not implemented yet")
		}
	}

	return &image{
		headerSize: headerSize,
		sections:   sections,
		size:       sectionsEnd,
		symbols:    symbs,
	}, nil
}

func (i *image) WriteTo(w io.Writer) (int64, error) {
	currentOffset := i.headerSize
	written := int64(0)

	for _, section := range i.sections {
		if section.addrInFile < currentOffset {
			panic(fmt.Sprintf("unexpected section address: %d address to write should not be less than current offset for writing %d", section.addrInFile, currentOffset))
		}

		// Sections might have gaps between them due to alignment; fill with zeros in this case
		// (as we can't assume the writer is zeroed, and we don't have Seek anyway)
		if section.addrInFile > currentOffset {
			// Write zeros to pad
			zerosWritten, err := w.Write(make([]byte, section.addrInFile-currentOffset))
			written += int64(zerosWritten)
			if err != nil {
				return written, fmt.Errorf("failed to write padding: %w", err)
			}

			currentOffset = section.addrInFile
		}

		if section.Type == elf.SHT_NOBITS {
			// Fill BSS section with zeros. The elf package will error if we try to read
			// from this section (which it probably should, to be fair)
			zerosWritten, err := w.Write(make([]byte, section.Size))
			written += int64(zerosWritten)
			if err != nil {
				return written, fmt.Errorf("failed to write NOBITS section: %w", err)
			}
		} else {
			// If not a BSS section, copy it to the writer
			reader := section.Open()

			sectionWritten, err := io.Copy(w, reader)
			written += sectionWritten
			if err != nil {
				return written, fmt.Errorf("failed to write section: %w", err)
			}

			if sectionWritten != int64(section.Size) {
				// This should theoretically never happen, unless someone is violating the
				// contract of io.Writer, or I messed up
				panic(fmt.Sprintf("invalid number of bytes written for section: expected %d, got %d", section.Size, sectionWritten))
			}
		}

		currentOffset += section.Size
	}

	return written, nil
}

func placeSections(f *elf.File, headerSize uint64) (imageSectionList, uint64, uint64) {
	addr := uint64(headerSize)
	imageSections := imageSectionList{}

	textSections := imageSectionList{}
	dataSections := imageSectionList{}
	bssSections := imageSectionList{}

	for sectionIndex, section := range f.Sections {
		hasExecInstr := section.Flags&elf.SHF_EXECINSTR > 0
		hasAlloc := section.Flags&elf.SHF_ALLOC > 0

		isection := &imageSection{Section: *section, index: sectionIndex}

		if hasExecInstr && hasAlloc {
			textSections = append(textSections, isection)
		} else if !hasExecInstr && hasAlloc {
			if section.Type == elf.SHT_NOBITS {
				bssSections = append(bssSections, isection)
			} else {
				dataSections = append(dataSections, isection)
			}
		}
	}

	// Concat sections of the same type in a specific order: first .text, then
	// .data, then .bss
	imageSections, addr = concatSections(imageSections, addr, textSections)
	imageSections, addr = concatSections(imageSections, addr, dataSections)
	bssStart := addr
	imageSections, addr = concatSections(imageSections, addr, bssSections)

	return imageSections, bssStart, addr
}

func concatSections(dest imageSectionList, addr uint64, source imageSectionList) (imageSectionList, uint64) {
	for _, section := range source {
		if section.Addralign > 0 {
			addr = align(addr, section.Addralign)
		}

		sectionWithAddr := *section
		sectionWithAddr.addrInFile = addr

		dest = append(dest, &sectionWithAddr)

		addr += section.Size
	}

	return dest, addr
}

func relocateSymbols(f *elf.File, sections imageSectionList, bssStart uint64, end uint64) ([]elf.Symbol, error) {
	symbs, err := f.Symbols()
	if err != nil {
		return nil, fmt.Errorf("failed to get symbols in file: %w", err)
	}

	relocatedSymbs := make([]elf.Symbol, 0, len(symbs))

	for _, symb := range symbs {
		if symb.Section == elf.SHN_UNDEF {
			if symb.Name == symbBssStart {
				symb.Value = bssStart
			} else if symb.Name == symbEnd {
				symb.Value = end
			} else {
				return nil, fmt.Errorf("error processing symbol '%s': %w", symb.Name, errUnrecognizedSymbol)
			}
		} else if symb.Section == elf.SHN_ABS {
			// Symbols are absolute, and we have no need to relocate them
		} else {
			section, err := sections.GetByIndex(int(symb.Section))
			if err != nil {
				return nil, fmt.Errorf("could not find section with index '%d' defined by symbol '%s': %w", symb.Section, symb.Name, err)
			}

			symb.Value = section.addrInFile + symb.Value
		}

		relocatedSymbs = append(relocatedSymbs, symb)
	}

	return relocatedSymbs, nil
}

func align(addr uint64, alignment uint64) uint64 {
	return ((addr + alignment - 1) / alignment) * alignment
}
