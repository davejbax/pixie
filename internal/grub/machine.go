package grub

import (
	"debug/elf"
	"debug/pe"
	"errors"

	"github.com/davejbax/pixie/internal/efipe"
)

var errUnsupportedELFMachineType = errors.New("unsupported ELF machine type")

func isMachineSupported(m elf.Machine) bool {
	// TODO aarch64 support
	return m == elf.EM_X86_64
}

func efipeMachine(m elf.Machine) (efipe.Machine, error) {
	switch m {
	case elf.EM_X86_64:
		return pe.IMAGE_FILE_MACHINE_AMD64, nil
	// case elf.EM_AARCH64: TODO aarch64 support
	// 	return pe.IMAGE_FILE_MACHINE_ARM64, nil
	default:
		return 0, errUnsupportedELFMachineType
	}
}
