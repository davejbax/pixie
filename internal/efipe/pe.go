package efipe

import (
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/davejbax/pixie/internal/align"
	"github.com/davejbax/pixie/internal/iometa"
	"github.com/lunixbochs/struc"
)

const (
	SectionText  = ".text"
	SectionData  = ".data"
	SectionBSS   = ".bss"
	sectionReloc = ".reloc"

	UEFIPageSize = 4096

	pe32PlusMagic = 0x020b

	// Statically define our (DOS header + PE32+ header) header size, as we
	// need to know this in advance. It's pretty unlikely that this will
	// exceed the UEFI page size
	totalHeaderSize = UEFIPageSize

	// Optional PE32+ header necessarily has 112 bytes, plus 8 bytes per data directory
	optionalHeaderSize = 112 + 8*numDataDirectories

	// We'll define 16 data directories, which is the number listed in
	// Microsoft docs [https://learn.microsoft.com/en-us/windows/win32/debug/pe-format#optional-header-data-directories-image-only]
	// I don't know if these are strictly necessary, but it's what GRUB
	// does, so ¯\_(ツ)_/¯
	// Also, we have to do this to use Go's [pe.OptionalHeader64] structure,
	// as this hardcodes the number as 16
	numDataDirectories = 16
)

var (
	// PE\0\0
	peMagic = []byte{0x50, 0x45, 0x00, 0x00}

	errNoTextSection        = errors.New("required .text section not found in provided executable sections")
	errSectionOffsetInvalid = errors.New("section offset is less than number of bytes already written")
)

type Image struct {
	dos       *dosImage
	header    *pe.FileHeader
	optHeader *pe.OptionalHeader64
	program   Executable
	sections  []Section
}

type Machine uint16

// Executable defines a relocatable executable that will be the
// 'payload' of a PE file
type Executable interface {
	Entrypoint() uint32
	BaseOfCode() uint32

	// Size here must include the expected size of the DOS + PE32 headers:
	// it is the size of the ENTIRE image file
	// TODO: do I want to change this so that it doesn't have to worry about DOS/PE32 header size? Probably not as more complex
	Size() uint32

	// Sections contained within the executable. Note that these must meet
	// a few requirements:
	//
	//  1. Sections must be defined in virtual address order
	//  2. Section virtual addresses must be aligned to [UEFIPageSize]
	//  3. Section physical addresses must be aligned to [UEFIPageSize]
	Sections() SectionList

	// PE machine type
	Machine() Machine

	// All address relocations that cannot be resolved by the linker, and must instead
	// be resolved by the PE loader
	Relocations() []*Relocation
}

func New(program Executable) (*Image, error) {
	textSection, found := program.Sections().GetByName(SectionText)
	if !found {
		return nil, errNoTextSection
	}

	dataSectionSize := uint32(0)
	for _, section := range program.Sections() {
		header := section.Header()
		if header.Characteristics&pe.IMAGE_SCN_CNT_INITIALIZED_DATA > 0 {
			dataSectionSize += header.Size
		}
	}

	bssSectionSize := uint32(0)
	if bssSection, found := program.Sections().GetByName(SectionBSS); found {
		// TODO: do we use virtual size here?
		bssSectionSize = bssSection.Header().Size
	}

	optHeader := pe.OptionalHeader64{
		Magic: pe32PlusMagic,

		// Unimportant
		MajorLinkerVersion: 0,
		MinorLinkerVersion: 0,

		SizeOfCode:              textSection.Header().Size,
		SizeOfInitializedData:   dataSectionSize,
		SizeOfUninitializedData: bssSectionSize,
		AddressOfEntryPoint:     program.Entrypoint(),
		BaseOfCode:              program.BaseOfCode(),
		ImageBase:               0, // No preference

		// Align sections in memory (SectionAlignment) and assume they are
		// aligned in the PE file (FileAlignment) to the UEFI page size.
		// This probably isn't strictly necessary, but it's what GRUB does,
		// and seems broadly sensible
		SectionAlignment: UEFIPageSize,
		FileAlignment:    UEFIPageSize,

		// Generally unimportant fields that are fine to leave as zeros
		MajorOperatingSystemVersion: 0,
		MinorOperatingSystemVersion: 0,
		MajorImageVersion:           0,
		MinorImageVersion:           0,
		MajorSubsystemVersion:       0,
		MinorSubsystemVersion:       0,
		Win32VersionValue:           0,

		SizeOfImage:   program.Size(),
		SizeOfHeaders: totalHeaderSize, // must be rounded up to FileAlignment

		// Unused
		CheckSum: 0,

		Subsystem:          pe.IMAGE_SUBSYSTEM_EFI_APPLICATION,
		DllCharacteristics: 0,

		// Use the same values as GRUB here. The GRUB source code contains the
		// comment 'do these really matter?' in relation to these, which is a question
		// that I'd also ask...
		SizeOfStackReserve: 65536,
		SizeOfStackCommit:  65536,
		SizeOfHeapReserve:  65536,
		SizeOfHeapCommit:   65536,

		// Reserved, must be zero
		LoaderFlags: 0,

		// Note that size is one of the fields for DataDirectory, so if we zero this,
		// then we shouldn't break anything (hopefully)
		// TODO: add relocations later
		NumberOfRvaAndSizes: numDataDirectories,
		DataDirectory:       [numDataDirectories]pe.DataDirectory{},
	}

	sections := program.Sections()

	if len(program.Relocations()) > 0 {
		lastSection := sections[len(sections)-1]
		relocStart := align.Address(lastSection.Header().Offset+lastSection.Header().Size, UEFIPageSize)
		relocSection := newRelocationSection(program.Relocations(), relocStart)
		sections = append(sections, relocSection)

		optHeader.SizeOfImage += relocSection.Header().Size
		optHeader.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_BASERELOC].Size = relocSection.Header().Size
		optHeader.DataDirectory[pe.IMAGE_DIRECTORY_ENTRY_BASERELOC].VirtualAddress = relocSection.Header().VirtualAddress
	}

	header := pe.FileHeader{
		Machine:          uint16(program.Machine()),
		NumberOfSections: uint16(len(sections)),

		// Unimportant; don't bother setting
		TimeDateStamp: 0,

		// Deprecated for images and therefore should be zero (these are debugging info)
		PointerToSymbolTable: 0,
		NumberOfSymbols:      0,

		SizeOfOptionalHeader: optionalHeaderSize,
		Characteristics:      pe.IMAGE_FILE_EXECUTABLE_IMAGE | pe.IMAGE_FILE_LOCAL_SYMS_STRIPPED | pe.IMAGE_FILE_DEBUG_STRIPPED | pe.IMAGE_FILE_LINE_NUMS_STRIPPED,
	}

	peHeaderStartAddr := align.Address(uint32(len(dosStub)), 128)
	dosImage := newDOSImage(dosStub, peHeaderStartAddr)

	return &Image{
		dos:       dosImage,
		header:    &header,
		optHeader: &optHeader,
		program:   program,
		sections:  sections,
	}, nil
}

func (i *Image) WriteTo(w io.Writer) (int64, error) {
	cw := &iometa.CountingWriter{Writer: w}

	if _, err := i.dos.WriteTo(cw); err != nil {
		return int64(cw.BytesWritten()), fmt.Errorf("failed to write DOS header: %w", err)
	}

	if _, err := cw.Write(peMagic); err != nil {
		return int64(cw.BytesWritten()), fmt.Errorf("failed to write PE magic: %w", err)
	}

	if err := struc.PackWithOptions(cw, i.header, &struc.Options{Order: binary.LittleEndian}); err != nil {
		return int64(cw.BytesWritten()), fmt.Errorf("failed to write PE header: %w", err)
	}

	if err := struc.PackWithOptions(cw, i.optHeader, &struc.Options{Order: binary.LittleEndian}); err != nil {
		return int64(cw.BytesWritten()), fmt.Errorf("failed to write PE optional header: %w", err)
	}

	for _, section := range i.sections {
		header := section.Header()

		// We need to convert to a [pe.SectionHeader32], which is like a [pe.SectionHeader]
		// but has the standard-defined 8-byte name for the section instead of a Go string
		header32 := pe.SectionHeader32{
			Name:                 sectionName(header.Name),
			VirtualSize:          header.VirtualSize,
			VirtualAddress:       header.VirtualAddress,
			SizeOfRawData:        header.Size,
			PointerToRawData:     header.Offset,
			PointerToRelocations: header.PointerToRelocations,
			PointerToLineNumbers: header.PointerToLineNumbers,
			NumberOfRelocations:  header.NumberOfRelocations,
			NumberOfLineNumbers:  header.NumberOfLineNumbers,
			Characteristics:      header.Characteristics,
		}

		if err := struc.PackWithOptions(cw, &header32, &struc.Options{Order: binary.LittleEndian}); err != nil {
			return int64(cw.BytesWritten()), fmt.Errorf("failed to write section '%s': %w", header.Name, err)
		}
	}

	for _, section := range i.sections {
		// Sections aren't necessarily contiguous and generally start on some power-of-two boundary.
		// Hence, we need to write zeros until we reach the start of the section.
		bytesUntilSection := int(section.Header().Offset) - cw.BytesWritten()
		if bytesUntilSection < 0 {
			return int64(cw.BytesWritten()), errSectionOffsetInvalid
		} else if bytesUntilSection > 0 {
			if err := iometa.WriteZeros(cw, bytesUntilSection); err != nil {
				return int64(cw.BytesWritten()), fmt.Errorf("failed to write zero padding before section: %w", err)
			}
		}

		reader := section.Open()
		written, err := io.Copy(cw, reader)
		if err != nil {
			return int64(cw.BytesWritten()), fmt.Errorf("failed to write PE section '%s': %w", section.Header().Name, err)
		}

		slog.Debug("wrote PE image section",
			"count", written,
			"section", section.Header().Name,
		)

		_ = reader.Close()
	}

	// The section end was probably aligned to some boundary, and this might be more data than they give us.
	// If that's the case, pad it with zeros.
	// TODO: should this be the responsibility of the section provider?
	lastSection := i.sections[len(i.sections)-1]
	bytesRemaining := int(lastSection.Header().Offset) + int(lastSection.Header().Size) - cw.BytesWritten()
	if bytesRemaining > 0 {
		if err := iometa.WriteZeros(cw, bytesRemaining); err != nil {
			return int64(cw.BytesWritten()), fmt.Errorf("failed to write final zero padding: %w", err)
		}
	}

	return int64(cw.BytesWritten()), nil
}

func sectionName(name string) [8]uint8 {
	if len(name) > 8 {
		name = name[:8]
	} else if len(name) < 8 {
		name += strings.Repeat("\x00", 8-len(name))
	}

	nameBytes := []uint8(name)
	return [8]uint8(nameBytes)
}

type Section interface {
	Open() io.ReadCloser
	Header() pe.SectionHeader
}

type SectionList []Section

func (sections SectionList) GetByName(name string) (Section, bool) {
	for _, section := range sections {
		if section.Header().Name == name {
			return section, true
		}
	}

	return nil, false
}
