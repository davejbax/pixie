package grub

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"

	"github.com/davejbax/pixie/internal/efipe"
	"github.com/lunixbochs/struc"
)

var (
	errBadSymbolIndex        = errors.New("symbol index out of symbol table range")
	errUnsupportedRelocation = errors.New("unsupported relocation type")
	errRelocationOutOfBounds = errors.New("relocation exceeds bounds of section")
)

type relocation struct {
	typ    uint32
	addend int64
	// offset relative to the start of the section
	offset uint64
	// offset relative to the start of the file
	fileOffset uint64
	symbValue  uint64
	symbIndex  uint32
}

func relocateAddresses(f *elf.File, virtualSections []*virtualSection, symbs []elf.Symbol) ([]*efipe.Relocation, error) {
	var typToFunc func(uint32) (relocationFunc, bool)
	switch f.Machine {
	case elf.EM_X86_64:
		typToFunc = func(typ uint32) (relocationFunc, bool) {
			f, ok := relocationFuncsX86_64[elf.R_X86_64(typ)]
			return f, ok
		}
	default:
		return nil, errUnsupportedELFMachineType
	}

	sectionsByIndex := make(map[int]*elfSection)
	for _, virt := range virtualSections {
		for _, section := range virt.realSections {
			sectionsByIndex[section.index] = section
		}
	}

	for _, section := range f.Sections {
		if section.Type != elf.SHT_REL && section.Type != elf.SHT_RELA {
			continue
		}

		hasAddend := section.Type == elf.SHT_RELA

		// Skip sections we're not keeping
		targetSection, ok := sectionsByIndex[int(section.Info)]
		if !ok {
			// TODO slog here
			slog.Warn("skipping ELF relocation section (references excluded section)",
				"section", section.Name,
				"targetSectionIndex", section.Info,
			)
			continue
		}

		reader := section.Open()

		numEntries := section.Size / section.Entsize

		for i := 0; i < int(numEntries); i++ {
			var relSymb, relTyp uint32
			var relOffset uint64
			var relAddend int64
			var err error

			if hasAddend {
				relSymb, relTyp, relOffset, relAddend, err = readRelaEntry(reader)
			} else {
				relSymb, relTyp, relOffset, err = readRelEntry(reader)
			}

			if err != nil {
				return nil, fmt.Errorf("failed to read relocation entry at index %d in %s: %w", i, section.Name, err)
			}

			if int(relSymb) >= len(symbs) {
				return nil, fmt.Errorf("symbol index %d >= symbol table size %d: %w", relSymb, len(symbs), errBadSymbolIndex)
			}

			if _, ok := typToFunc(relTyp); !ok {
				return nil, fmt.Errorf("could not get relocation function for type '%d': %w", relTyp, errUnsupportedRelocation)
			}

			targetSection.relocationTypToFunc = typToFunc
			targetSection.relocations = append(targetSection.relocations, &relocation{
				typ:        relTyp,
				addend:     relAddend,
				offset:     relOffset,
				fileOffset: targetSection.addrInFile + relOffset,
				symbValue:  symbs[int(relSymb)].Value,
				symbIndex:  relSymb,
			})
		}
	}

	var unresolvedRelocs []*efipe.Relocation

	// Now that we've created lists of all relocation entries for all sections, process
	// the relocations to form the [efipe.Relocation]s that we need for the PE file.
	// This is slightly inefficient, as we'll be re-doing this when we read the virtual
	// sections, but the tradeoff here is memory consumption: the alternative would be
	// storing all rewritten sections in memory until we later come to read the virtual
	// sections. Hence, we trade a bit of IO inefficiency for lower peak memory consumption
	// and earlier garbage collection.
	for _, virt := range virtualSections {
		for _, section := range virt.realSections {
			if len(section.relocations) > 0 {
				_, relocs, err := section.processRelocations()
				if err != nil {
					return nil, fmt.Errorf("failed to preprocess relocations for section '%s': %w", section.Name, err)
				}

				unresolvedRelocs = append(unresolvedRelocs, relocs...)
			}
		}
	}

	return unresolvedRelocs, nil
}

func (section *elfSection) processRelocations() ([]byte, []*efipe.Relocation, error) {
	reader := section.Open()
	data, err := io.ReadAll(reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read section data for relocation: %w", err)
	}

	slog.Debug("processing ELF relocation section",
		"section", section.Name,
	)

	var unresolvedRelocs []*efipe.Relocation

	for _, relocation := range section.relocations {
		f, ok := section.relocationTypToFunc(relocation.typ)
		if !ok {
			// TODO: should really make this an actual error type...
			return nil, nil, errUnsupportedRelocation
		}

		if relocation.offset >= uint64(len(data)) {
			return nil, nil, errRelocationOutOfBounds
		}

		unresolvedReloc, err := f(data[relocation.offset:], relocation)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to do relocation: %w", err)
		}

		if unresolvedReloc != nil {
			unresolvedRelocs = append(unresolvedRelocs, unresolvedReloc)
		}
	}

	return data, unresolvedRelocs, nil
}

func readRelEntry(r io.Reader) (uint32, uint32, uint64, error) {
	var rel elf.Rel64

	if err := struc.UnpackWithOptions(r, &rel, &struc.Options{Order: binary.LittleEndian}); err != nil {
		return 0, 0, 0, fmt.Errorf("failed to unpack Rel64 entry: %w", err)
	}

	relSymb, relType := relocationInfo(rel.Info)
	return relSymb, relType, rel.Off, nil
}

func readRelaEntry(r io.Reader) (uint32, uint32, uint64, int64, error) {
	var rel elf.Rela64

	if err := struc.UnpackWithOptions(r, &rel, &struc.Options{Order: binary.LittleEndian}); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("failed to unpack Rela64 entry: %w", err)
	}

	relSymb, relType := relocationInfo(rel.Info)
	return relSymb, relType, rel.Off, rel.Addend, nil
}

func relocationInfo(info uint64) (sym uint32, typ uint32) {
	return uint32(info >> 32), uint32(info & 0xFFFFFFFF)
}

// Wraps an [io.Reader] and rewrites relocated addresses
type relocationReader struct {
	data []byte
}

func newRelocationReader(section *elfSection) (*relocationReader, error) {
	data, _, err := section.processRelocations()
	if err != nil {
		return nil, fmt.Errorf("failed to process section relocations: %w", err)
	}

	return &relocationReader{data: data}, nil
}

func (r *relocationReader) Read(dst []byte) (int, error) {
	read := copy(dst, r.data)
	r.data = r.data[read:]

	if len(r.data) == 0 {
		return read, io.EOF
	}

	return read, nil
}

type relocationFunc = func([]byte, *relocation) (*efipe.Relocation, error)

var relocationFuncsX86_64 = map[elf.R_X86_64]relocationFunc{
	elf.R_X86_64_NONE: relocateNoop,
	elf.R_X86_64_64:   relocateX86_64Adapter(relocateX86_64_64),
	elf.R_X86_64_PC32: relocateX86_64Adapter(relocateX86_64_PC32),
	// We're only ever dealing with a statically-linked binary, so we can reduce PLT32
	// down to PC32. I don't fully understand this, but the kernel wizards say it's okay:
	// https://git.kernel.org/pub/scm/linux/kernel/git/torvalds/linux.git/commit/?id=b21ebf2fb4cde1618915a97cc773e287ff49173e
	elf.R_X86_64_PLT32: relocateX86_64Adapter(relocateX86_64_PC32),
}

func relocateNoop(_ []byte, _ *relocation) (*efipe.Relocation, error) {
	return nil, nil
}

func relocateX86_64Adapter[N int64 | int32](relocator func(N, *relocation) (N, *efipe.Relocation)) relocationFunc {
	return func(out []byte, rel *relocation) (*efipe.Relocation, error) {
		var addr N
		if err := struc.UnpackWithOptions(bytes.NewReader(out), &addr, &struc.Options{Order: binary.LittleEndian}); err != nil {
			return nil, fmt.Errorf("invalid relocation: %w", err)
		}

		oldAddr := addr
		var unresolvedReloc *efipe.Relocation
		addr, unresolvedReloc = relocator(addr, rel)

		slog.Debug("relocating ELF X86_64 entry",
			"type", rel.typ,
			"symbIndex", rel.symbIndex,
			"symbValue", fmt.Sprintf("0x%02x", rel.symbValue),
			"addend", fmt.Sprintf("0x%02x", rel.addend),
			"offset", fmt.Sprintf("0x%02x", rel.fileOffset),
			"from", fmt.Sprintf("0x%02x", oldAddr),
			"to", fmt.Sprintf("0x%02x", addr),
		)

		buff := &bytes.Buffer{}
		if err := struc.PackWithOptions(buff, addr, &struc.Options{Order: binary.LittleEndian}); err != nil {
			return nil, fmt.Errorf("failed to write new relocation value to buffer: %w", err)
		}

		copy(out, buff.Bytes())
		return unresolvedReloc, nil
	}
}

func relocateX86_64_64(addr int64, rel *relocation) (int64, *efipe.Relocation) {
	// Note: we lose the top bit going from unsigned to signed here, but we probably
	// are never going to have a value that's going to hit 2^63... right?
	addr += int64(rel.symbValue) + rel.addend

	peRel := efipe.Relocation{
		Kind:       efipe.ImageRelBasedDir64,
		FileOffset: rel.fileOffset,
	}

	return addr, &peRel
}

func relocateX86_64_PC32(addr int32, rel *relocation) (int32, *efipe.Relocation) { //nolint:revive
	// PC = section address in file + rel offset
	return addr + int32(rel.addend&0xFFFFFFFF) + int32(rel.symbValue&0xFFFFFFFF) - int32(rel.fileOffset&0xFFFFFFFF), nil
}
