package grub

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type moduleDependencies map[string][]string

var (
	errInvalidDependencyListFormat = errors.New("dependency list does not follow GRUB moddep.lst format")
	errUnrecognizedModule          = errors.New("unrecognised module name")
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
	size    uint32
	reader  io.ReadCloser
}

func ReadModuleFromDirectory(directory string, module string) (*Module, error) {
	path := filepath.Join(directory, module+".mod")

	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed to stat module '%s' from path '%s': %w", module, path, err)
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("could not open module '%s' from path '%s': %w", module, path, err)
	}

	return &Module{
		objType: ObjTypeElf, // TODO: make this a param? Do we ever want to read a non-elf file from disk?
		size:    uint32(stat.Size()),
		reader:  file,
	}, err
}
