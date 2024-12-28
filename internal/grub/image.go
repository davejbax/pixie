package grub

import (
	"debug/elf"
	"errors"
	"fmt"
	"io"

	"github.com/davejbax/pixie/internal/align"
	"github.com/davejbax/pixie/internal/efipe"
)

const (
	// Name of unreferenced BSS start symbol
	symbBssStart = "__bss_start"

	// Name of special unreferenced 'end' symbol
	symbEnd = "end"

	// Name of the entrypoint symbol
	symbStart = "_start"
)

var (
	errSectionNotFound    = errors.New("section with given index not found")
	errUnrecognizedSymbol = errors.New("unrecognised symbol defined relative to SHN_UNDEF")
	errNoEntrypoint       = errors.New("image has no entrypoint")
)

type image struct {
	file            *elf.File
	headerSize      uint64
	size            uint64
	symbols         []elf.Symbol
	virtualSections []*virtualSection
	relocations     []*efipe.Relocation
}

var _ efipe.Executable = &image{}

// TODO: document properly
// alignment must be a power of two
func NewImage(r io.ReaderAt, headerSize uint64, alignment uint64) (*image, error) {
	elfFile, err := elf.NewFile(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read ELF file: %w", err)
	}

	if !isMachineSupported(elfFile.Machine) {
		return nil, errUnsupportedELFMachineType
	}

	virtualSections := layoutVirtualSections(elfFile, headerSize, alignment)

	symbs, err := relocateSymbols(elfFile, virtualSections)
	if err != nil {
		return nil, fmt.Errorf("failed to relocate symbols: %w", err)
	}

	relocs, err := relocateAddresses(elfFile, virtualSections, symbs)
	if err != nil {
		return nil, fmt.Errorf("failed to relocate addresses: %w", err)
	}

	foundStart := false
	for _, symb := range symbs {
		if symb.Name == symbStart {
			foundStart = true
			break
		}
	}

	if !foundStart {
		return nil, errNoEntrypoint
	}

	lastSection := virtualSections[len(virtualSections)-1]
	// Realign the end of the sections to whatever the requested boundary is
	end := align.Address(lastSection.offset+lastSection.size, alignment)

	return &image{
		file:            elfFile,
		headerSize:      headerSize,
		size:            end,
		symbols:         symbs,
		virtualSections: virtualSections,
		relocations:     relocs,
	}, nil
}

func (i *image) Entrypoint() uint32 {
	for _, symb := range i.symbols {
		if symb.Name == symbStart {
			return uint32(symb.Value)
		}
	}

	panic("could not find entrypoint symbol")
}

func (i *image) BaseOfCode() uint32 {
	for _, virt := range i.virtualSections {
		if virt.kind == virtualSectionTypeText {
			return uint32(virt.offset)
		}
	}

	panic("no .text section found")
}

func (i *image) Machine() efipe.Machine {
	machine, err := efipeMachine(i.file.Machine)
	if err != nil {
		panic(fmt.Sprintf("could not convert ELF machine type to EFI PE machine: %v", err))
	}

	return machine
}

func (i *image) Sections() efipe.SectionList {
	sections := make([]efipe.Section, 0, len(i.virtualSections))

	for _, section := range i.virtualSections {
		sections = append(sections, section)
	}

	return sections
}

func (i *image) Size() uint32 {
	return uint32(i.size)
}

func (i *image) Relocations() []*efipe.Relocation {
	return i.relocations
}
