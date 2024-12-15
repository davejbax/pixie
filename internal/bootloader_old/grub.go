package bootloader

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sync/errgroup"
)

const DefaultGrubDownloadURLTemplate = "https://ftp.gnu.org/gnu/grub/grub-{{ .Version }}.tar.xz"

var (
	ErrBootloaderDirectoryIsNotDir = errors.New("bootloader directory exists but is not a directory")
	ErrCannotExtractArchiveType    = errors.New("do not know how to extract archive type")
	ErrInsecurePath                = errors.New("archive contains non-local files (insecure due to path traversal risk)")
)

type GrubConfig struct {
	DownloadURLTemplate                 string
	DownloadArchiveHasTopLevelDirectory bool
	Version                             string
	RootStorageDirectory                string
}

type GrubConfigOption func(*GrubConfig)

func WithGrubDownloadURLTemplate(downloadURLTemplate string) GrubConfigOption {
	return func(config *GrubConfig) {
		// TODO: validate the template here?
		config.DownloadURLTemplate = downloadURLTemplate
	}
}

func NewGrubConfig(rootStorageDirectory string, version string, options ...GrubConfigOption) *GrubConfig {
	config := &GrubConfig{
		RootStorageDirectory:                rootStorageDirectory,
		Version:                             version,
		DownloadURLTemplate:                 DefaultGrubDownloadURLTemplate,
		DownloadArchiveHasTopLevelDirectory: true,
	}

	for _, opt := range options {
		opt(config)
	}

	return config
}

func (c *GrubConfig) StorageDirectory() string {
	return filepath.Join(c.RootStorageDirectory, c.Version)
}

func LoadGrubOrDownload(ctx context.Context, config *GrubConfig) (*Grub, error) {
	grub, err := LoadGrub(config)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("unexpected error encountered while attempting to load grub: %w", err)
	} else if err == nil {
		return grub, nil
	}

	return DownloadGrub(ctx, config)
}

func LoadGrub(config *GrubConfig) (*Grub, error) {
	dir := config.StorageDirectory()

	fi, err := os.Stat(dir)
	if err == nil {
		if !fi.IsDir() {
			return nil, fmt.Errorf("failed to load grub directory '%s': %w", dir, ErrBootloaderDirectoryIsNotDir)
		}

		return &Grub{
			directory: dir,
		}, nil
	}

	return nil, fmt.Errorf("failed to stat grub directory: %w", err)
}

func DownloadGrub(ctx context.Context, config *GrubConfig) (*Grub, error) {
	url, err := downloadURL(config.DownloadURLTemplate, &DownloadOptions{Version: config.Version})
	if err != nil {
		return nil, fmt.Errorf("failed to generate download URL: %w", err)
	}

	if err := os.MkdirAll(config.StorageDirectory(), 0o755); err != nil {
		return nil, fmt.Errorf("could not create directory for storing grub: %w", err)
	}

	reader, writer := io.Pipe()
	eg := &errgroup.Group{}

	eg.Go(extractor(ctx, reader, "", config.StorageDirectory(), config.DownloadArchiveHasTopLevelDirectory))

	eg.Go(func() error {
		return download(ctx, writer, url)
	})

	if err := eg.Wait(); err != nil {
		// TODO: clean up here
		return nil, err
	}

	return &Grub{
		directory: config.StorageDirectory(),
	}, nil
}

type UnsupportedFileError struct {
	mode fs.FileMode
	name string
}

func (e *UnsupportedFileError) Error() string {
	return fmt.Sprintf("cannot extract file with unsupported mode '%s': %s", e.mode.String(), e.name)
}

type Grub struct {
	directory string
}
