package efipe

import "debug/pe"

// Mapping of PE machine type to filename expected by UEFI for automatic boot application loading
var ImageFileName = map[Machine]string{
	pe.IMAGE_FILE_MACHINE_AMD64: "BOOTx64.EFI",
	pe.IMAGE_FILE_MACHINE_I386:  "BOOTA32.EFI",
	pe.IMAGE_FILE_MACHINE_ARM64: "BOOTAA64.EFI",
	pe.IMAGE_FILE_MACHINE_ARM:   "BOOTARM.EFI",
}
