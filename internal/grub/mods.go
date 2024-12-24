package grub

import (
	"bufio"
	"errors"
	"fmt"
	"io"
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
