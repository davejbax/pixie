package grub

import (
	"bufio"
	"bytes"
	"debug/pe"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/davejbax/pixie/internal/align"
	"github.com/davejbax/pixie/internal/iometa"
	"github.com/lunixbochs/struc"
)

type moduleDependencies map[string][]string

var (
	errInvalidDependencyListFormat = errors.New("dependency list does not follow GRUB moddep.lst format")
	errUnrecognizedModule          = errors.New("unrecognised module name")
)

const (
	// XXX: This assumes 64-bit, which is currently all we support
	// We'll probably need to ask what target we're building for when creating
	// new modules to set this based on target pointer size (e.g. 4 for 32-bit)
	voidPointerAlignment = 8

	sectionMods = "mods"
)

func NewDependencyList(r io.Reader) (moduleDependencies, error) {
	list := make(moduleDependencies)
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := scanner.Text()

		// Format is "<module>: <dep1> <dep2> ..."
		// There may be no dependencies for a module
		module, depString, found := strings.Cut(line, ":")
		if !found {
			return nil, errInvalidDependencyListFormat
		}

		depString = strings.TrimSpace(depString)
		if depString == "" {
			list[module] = nil
			continue
		}

		list[module] = strings.Split(depString, " ")
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to read lines from moddep.lst: %w", err)
	}

	return list, nil
}

func (d moduleDependencies) Resolve(modules []string) ([]string, error) {
	unresolved := slices.Clone(modules)
	allDependencies := make([]string, 0, len(unresolved))

	for len(unresolved) > 0 {
		// Shift the module off the queue
		module := unresolved[0]
		unresolved = unresolved[1:]

		// A module itself counts as a dependency, since we're resolving
		// the full tree here
		allDependencies = append(allDependencies, module)

		directDependencies, ok := d[module]
		if !ok {
			return nil, fmt.Errorf("failed to get dependencies of '%s': %w", module, errUnrecognizedModule)
		}

		unresolved = append(unresolved, directDependencies...)
	}

	return allDependencies, nil
}

type ObjType uint32

const (
	ObjTypeElf ObjType = iota
	ObjTypeMemdisk
	ObjTypeConfig
	ObjTypePrefix
	ObjTypePubKey
	ObjTypeDTB
	ObjTypeDisableShimLock
	ObjTypeGPGPubKey
	ObjTypeX509PubKey
)

type Module struct {
	objType ObjType
	// Size of module payload, not including headers etc.
	payloadSize uint32
	open        func() (io.ReadCloser, error)
}

func ReadModuleFromDirectory(directory string, module string) (*Module, error) {
	path := filepath.Join(directory, module+".mod")

	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat module '%s' from path '%s': %w", module, path, err)
	}

	return &Module{
		objType:     ObjTypeElf, // TODO: make this a param? Do we ever want to read a non-elf file from disk?
		payloadSize: uint32(stat.Size()),
		open: func() (io.ReadCloser, error) {
			return os.Open(path)
		},
	}, err
}

const (
	moduleInfoMagic        = 0x676d696d    // gmim (GRUB module info magic)
	moduleInfoStructSize   = 4 + 4 + 8 + 8 // size of info structure
	moduleHeaderStructSize = 4 + 4         // two uint32s
)

type moduleInfo struct {
	// Magic number to indicate presence of modules
	Magic uint32

	Padding uint32

	// Offset of the modules relative to the start of this header
	Offset uint64

	// Size of all modules plus this header
	Size uint64
}

type moduleHeader struct {
	// Type of object
	Typ ObjType

	// Size of object, including this header
	Size uint32
}

func NewPrefixModule(prefix string) *Module {
	prefixLength := align.Address(uint32(len(prefix)+1), 8)
	prefixBytes := make([]byte, prefixLength)
	copy(prefixBytes, []byte(prefix))

	return &Module{
		objType:     ObjTypePrefix,
		payloadSize: uint32(prefixLength),
		open: func() (io.ReadCloser, error) {
			return &iometa.Closifier{Reader: bytes.NewReader(prefixBytes)}, nil
		},
	}
}

type moduleSection struct {
	data   []byte
	offset uint32
	vsize  uint32
}

func (s *moduleSection) Header() pe.SectionHeader {
	return pe.SectionHeader{
		Name:                 sectionMods,
		VirtualSize:          s.vsize,
		VirtualAddress:       s.offset,
		Size:                 s.vsize,
		Offset:               s.offset,
		PointerToRelocations: 0,
		PointerToLineNumbers: 0,
		NumberOfRelocations:  0,
		NumberOfLineNumbers:  0,
		Characteristics:      pe.IMAGE_SCN_CNT_INITIALIZED_DATA | pe.IMAGE_SCN_MEM_READ | pe.IMAGE_SCN_MEM_WRITE,
	}
}

// TODO make WriterTos instead of Readers!
func (s *moduleSection) Open() io.ReadCloser {
	return &iometa.Closifier{Reader: bytes.NewReader(s.data)}
}

func newModuleSection(mods []*Module, offset uint32, alignment uint32) (*moduleSection, error) {
	w := &bytes.Buffer{}

	size := uint64(0)
	for _, mod := range mods {
		size += uint64(mod.payloadSize) + moduleHeaderStructSize
	}

	info := &moduleInfo{
		Magic:   moduleInfoMagic,
		Padding: 0,
		Offset:  moduleInfoStructSize,
		Size:    moduleInfoStructSize + size,
	}

	if err := struc.PackWithOptions(w, info, &struc.Options{Order: binary.LittleEndian}); err != nil {
		return nil, err
	}

	for _, mod := range mods {
		header := &moduleHeader{Typ: mod.objType, Size: moduleHeaderStructSize + mod.payloadSize}
		if err := struc.PackWithOptions(w, header, &struc.Options{Order: binary.LittleEndian}); err != nil {
			return nil, err
		}

		payload, err := mod.open()
		if err != nil {
			return nil, fmt.Errorf("failed to open module for reading: %w", err)
		}
		defer payload.Close()

		if _, err := io.Copy(w, payload); err != nil {
			return nil, fmt.Errorf("failed to read module payload: %w", err)
		}
	}

	return &moduleSection{data: w.Bytes(), offset: offset, vsize: align.Address(uint32(w.Len()), alignment)}, nil
}
