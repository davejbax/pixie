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
	errUnrecognizedSymbol = errors.New("unrecognised symbol defined relative to SHN_UNDEF")
	errNoEntrypoint       = errors.New("image has no entrypoint")
)

type Image struct {
	file            *elf.File
	headerSize      uint32
	size            uint32
	symbols         []elf.Symbol
	virtualSections []*virtualSection
	relocations     []*efipe.Relocation
	modules         *moduleSection
}

var _ efipe.Executable = &Image{}

// TODO: document properly
// alignment must be a power of two
func NewImage(r io.ReaderAt, mods []*Module, alignment uint32) (*Image, error) {
	elfFile, err := elf.NewFile(r)
	if err != nil {
		return nil, fmt.Errorf("failed to read ELF file: %w", err)
	}

	if !isMachineSupported(elfFile.Machine) {
		return nil, errUnsupportedELFMachineType
	}

	// Allow enough room for 3 sections -- .text, .data, and mods (even though we
	// might not have mods!)
	headerSize := efipe.PEHeaderSize(3)

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
	end := align.Address(uint32(lastSection.offset+lastSection.size), alignment)

	var moduleSection *moduleSection

	if len(mods) > 0 {
		moduleSection = newModuleSection(mods, end, alignment)
		end = align.Address(end+moduleSection.Header().VirtualSize, alignment)
	}

	return &Image{
		file:            elfFile,
		headerSize:      headerSize,
		size:            end,
		symbols:         symbs,
		virtualSections: virtualSections,
		relocations:     relocs,
		modules:         moduleSection,
	}, nil
}

func (i *Image) PEHeaderSize() uint32 {
	return i.headerSize
}

func (i *Image) Entrypoint() uint32 {
	for _, symb := range i.symbols {
		if symb.Name == symbStart {
			return uint32(symb.Value)
		}
	}

	panic("could not find entrypoint symbol")
}

func (i *Image) BaseOfCode() uint32 {
	for _, virt := range i.virtualSections {
		if virt.kind == virtualSectionTypeText {
			return uint32(virt.offset)
		}
	}

	panic("no .text section found")
}

func (i *Image) Machine() efipe.Machine {
	machine, err := efipeMachine(i.file.Machine)
	if err != nil {
		panic(fmt.Sprintf("could not convert ELF machine type to EFI PE machine: %v", err))
	}

	return machine
}

func (i *Image) Sections() efipe.SectionList {
	sections := make([]efipe.Section, 0, len(i.virtualSections))

	for _, section := range i.virtualSections {
		sections = append(sections, section)
	}

	if i.modules != nil {
		sections = append(sections, i.modules)
	}

	return sections
}

func (i *Image) Size() uint32 {
	return i.size
}

func (i *Image) Relocations() []*efipe.Relocation {
	return i.relocations
}
