package iso

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"github.com/davejbax/pixie/internal/align"
	"github.com/davejbax/pixie/internal/efipe"
	"github.com/diskfs/go-diskfs"
	"github.com/diskfs/go-diskfs/backend/file"
	"github.com/diskfs/go-diskfs/disk"
	"github.com/diskfs/go-diskfs/filesystem"
	"github.com/diskfs/go-diskfs/filesystem/iso9660"
)

var (
	errEntrypointAlreadyExists      = errors.New("already added entrypoint for given machine type")
	errUnsupportedEntrypointMachine = errors.New("entrypoint machine type is unsupported")
)

const (
	fatPadding   = 1024 // Bytes needed on top of total raw entrypoint(s) size for FAT headers etc.
	fat32MinSize = 33 * 1024 * 1024

	isoBlockSize = 2048

	// I have completely made these numbers up (bytes)
	isoOverheadPerFile = 1024
	isoOverhead        = 1024
	fatOverheadPerFile = 512
	fatOverhead        = 512
	fatAlign           = 512

	espBootDirectory = "/EFI/BOOT"
)

type Builder struct {
	tempDir     string
	entrypoints map[efipe.Machine]Entrypoint
}

func NewBuilder(tempDir string) *Builder {
	return &Builder{
		tempDir:     tempDir,
		entrypoints: make(map[efipe.Machine]Entrypoint),
	}
}

type Entrypoint interface {
	io.WriterTo
	Size() uint32
}

func (b *Builder) AddEFIEntrypoint(image Entrypoint, machine efipe.Machine) error {
	if _, ok := b.entrypoints[machine]; ok {
		return errEntrypointAlreadyExists
	}

	b.entrypoints[machine] = image
	return nil
}

func (b *Builder) entrypointSizes() []uint32 {
	sizes := make([]uint32, 0, len(b.entrypoints))
	for _, entrypoint := range b.entrypoints {
		sizes = append(sizes, entrypoint.Size())
	}
	return sizes
}

func (b *Builder) Build(output io.Writer) error {
	espFile, err := os.CreateTemp(b.tempDir, "esp-*.img")
	if err != nil {
		return fmt.Errorf("failed to create temporary FAT ESP file for writing: %w", err)
	}
	defer espFile.Close()
	defer os.Remove(espFile.Name())

	// Guess the size we'll need for the ESP FAT file based on very dubious logic
	espSize := uint64(guessSize(b.entrypointSizes(), fatOverheadPerFile, fatOverhead, fatAlign))

	if err := espFile.Truncate(int64(max(espSize, fat32MinSize))); err != nil {
		return fmt.Errorf("failed to resize FAT image: %w", err)
	}

	if err := b.buildESP(espFile); err != nil {
		return fmt.Errorf("failed to build ESP: %w", err)
	}

	isoFile, err := os.CreateTemp(b.tempDir, "pixie-*.iso")
	if err != nil {
		return fmt.Errorf("failed to create temporary ISO file for writing: %w", err)
	}
	defer isoFile.Close()
	defer os.Remove(isoFile.Name())

	// Guess the size of the ISO based on even more dubious logic
	isoSize := guessSize([]uint64{espSize}, isoOverheadPerFile, isoOverhead, isoBlockSize)

	if err := isoFile.Truncate(int64(isoSize)); err != nil {
		return fmt.Errorf("failed to resize ISO image: %w", err)
	}

	if err := b.buildISO(isoFile, espFile); err != nil {
		return fmt.Errorf("failed to build ISO: %w", err)
	}

	if _, err := io.Copy(output, isoFile); err != nil {
		return fmt.Errorf("failed to write ISO to output: %w", err)
	}

	return nil
}

func (b *Builder) buildESP(f *os.File) error {
	espDisk, err := diskfs.OpenBackend(file.New(f, false))
	if err != nil {
		return fmt.Errorf("failed to open FAT file as filesystem: %w", err)
	}

	espFs, err := espDisk.CreateFilesystem(disk.FilesystemSpec{
		Partition: 0,
		FSType:    filesystem.TypeFat32,
	})
	if err != nil {
		return fmt.Errorf("failed to create FAT32 filesystem: %w", err)
	}

	if err := mkdirs(espFs, espBootDirectory); err != nil {
		return fmt.Errorf("failed to create EFI boot directories: %w", err)
	}

	// Create /EFI/BOOT/BOOT<machine>.EFI for all entrypoints
	for machine, entrypoint := range b.entrypoints {
		filename, ok := efipe.ImageFileName[machine]
		if !ok {
			return fmt.Errorf("cannot detect image file name for machine type 0x%02x: %w", machine, errUnsupportedEntrypointMachine)

		}

		filepath := path.Join(espBootDirectory, filename)
		file, err := espFs.OpenFile(filepath, os.O_CREATE|os.O_RDWR)
		if err != nil {
			return fmt.Errorf("failed to open '%s': %w", filepath, err)
		}

		if _, err := entrypoint.WriteTo(file); err != nil {
			return fmt.Errorf("failed to write entrypoint for machine type 0x%02x: %w", machine, err)
		}
	}

	return nil
}

func (b *Builder) buildISO(f *os.File, esp io.Reader) error {
	isoDisk, err := diskfs.OpenBackend(file.New(f, false))
	if err != nil {
		return fmt.Errorf("failed to open ISO file as filesystem: %w", err)
	}

	// Logical blocksize MUST be 2048 to work with some UEFI firmware (EDK2 requires this).
	// Either that, or the go-diskfs library is broken somehow. (Without this, the ISO won'
	// boot using QEMU + EDK2 OVMF)
	isoDisk.LogicalBlocksize = isoBlockSize

	isoFs, err := isoDisk.CreateFilesystem(disk.FilesystemSpec{
		Partition:   0, // 0 = create filesystem on entire image
		FSType:      filesystem.TypeISO9660,
		VolumeLabel: "pixie",
	})
	if err != nil {
		return fmt.Errorf("failed to create ISO filesystem: %w", err)
	}

	espFile, err := isoFs.OpenFile("ESP.IMG", os.O_CREATE|os.O_RDWR)
	if err != nil {
		return fmt.Errorf("failed to create ESP image in ISO filesystem: %w", err)
	}

	if _, err := io.Copy(espFile, esp); err != nil {
		return fmt.Errorf("failed to write ESP image file: %w", err)
	}

	iso, ok := isoFs.(*iso9660.FileSystem)
	if !ok {
		panic("ISO filesystem should be iso9660.FileSystem, but it is not; possible bug in go-diskfs")
	}

	if err := iso.Finalize(iso9660.FinalizeOptions{
		VolumeIdentifier: "pixie",
		ElTorito: &iso9660.ElTorito{
			Platform: iso9660.EFI,
			Entries: []*iso9660.ElToritoEntry{
				{
					Platform:  iso9660.EFI,
					BootFile:  "ESP.IMG",
					Emulation: iso9660.NoEmulation,
				},
			},
		},
	}); err != nil {
		return fmt.Errorf("failed to finalize ISO: %w", err)
	}

	return nil
}

func guessSize[T uint32 | uint64 | int](fileSizes []T, overheadPerFile T, fixedOverhead T, alignment T) T {
	var size T

	for _, fileSize := range fileSizes {
		size += align.Address(fileSize+overheadPerFile, alignment)
	}

	size += fixedOverhead

	return size
}

func mkdirs(fs filesystem.FileSystem, path string) error {
	dir := ""

	for _, part := range strings.Split(path, "/") {
		if part == "" {
			continue
		}

		dir += "/" + part

		if err := fs.Mkdir(dir); err != nil {
			return fmt.Errorf("failed to make directory '%s': %w", dir, err)
		}
	}

	return nil
}
