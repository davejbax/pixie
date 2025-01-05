package distro

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/creasty/defaults"
	"github.com/go-viper/mapstructure/v2"
	"golang.org/x/sync/errgroup"
)

const (
	providerRocky = "rocky"

	metadataFilename = "pixie-metadata.json"
)

type Config struct {
	Provider        string
	Version         string
	Arch            []string
	ProviderOptions map[string]interface{} `mapstructure:",remain"`
}

var (
	errUnsupportedProvider = errors.New("unsupported provider")
)

type metadata struct {
	Hash string

	// Path of Linux kernel, relative to download directory
	KernelPath string

	// Path of initrd file relative to download directory
	InitrdPath string

	// Arbitrary provider-specific data
	ProviderData map[string]interface{}
}

type downloader interface {
	Hash() string
	HasDrifted(metadata *metadata) (bool, error)
	Download(directory string) (*metadata, error)
}

type provider interface {
	Latest(arch []string) (map[string]downloader, error)
}

type Manager struct {
	logger *slog.Logger

	arches           map[string][]string
	providers        map[string]provider
	storageDirectory string
}

// NewManager creates a new distro manager. A distro manager takes a config with the
// desired state of installed distros, and provides methods to check whether the
// installation state matches the desired state, and to reconcile this.
func NewManager(logger *slog.Logger, storageDirectory string, distros map[string]*Config) (*Manager, error) {
	providers := make(map[string]provider)
	arches := make(map[string][]string)

	for name, config := range distros {
		switch config.Provider {
		case providerRocky:
			opts, err := decodeProviderConfig[rockyOptions](config.ProviderOptions)
			if err != nil {
				return nil, fmt.Errorf("could not parse provider config for distro '%s': %w", name, err)
			}

			provider, err := newRocky(logger, config.Version, nil, opts)
			if err != nil {
				return nil, fmt.Errorf("failed to create Rocky provider: %w", err)
			}

			providers[name] = provider
			arches[name] = config.Arch
		default:
			return nil, fmt.Errorf("could not create provider for distro %s: %w", name, errUnsupportedProvider)
		}
	}

	return &Manager{
		logger: logger,

		arches:           arches,
		providers:        providers,
		storageDirectory: storageDirectory,
	}, nil
}

func decodeProviderConfig[T interface{}](opts map[string]interface{}) (*T, error) {
	var output T

	if err := defaults.Set(&output); err != nil {
		return nil, fmt.Errorf("failed to set default provider options: %w", err)
	}

	if err := mapstructure.Decode(opts, &output); err != nil {
		return nil, fmt.Errorf("failed to parse provider options: %w", err)
	}

	return &output, nil
}

func (m *Manager) Reconcile(parallelism int) ([]*Distro, error) {
	eg := &errgroup.Group{}
	eg.SetLimit(parallelism)

	distroCh := make(chan *Distro)
	distros := []*Distro{}

	go func() {
		for distro := range distroCh {
			distros = append(distros, distro)
		}
	}()

	for name, provider := range m.providers {
		arches := m.arches[name]

		m.logger.Debug("checking latest version of distro",
			"distro", name,
			"arches", arches,
		)

		downloaders, err := provider.Latest(arches)
		if err != nil {
			return nil, fmt.Errorf("failed to get latest version for distro %s: %w", name, err)
		}

		for arch, downloader := range downloaders {
			eg.Go(func() error {
				distro, err := m.reconcileForArch(name, arch, downloader)
				if err != nil {
					return fmt.Errorf("failed to reconcile distro '%s': %w", name, err)
				}

				distroCh <- distro
				return nil
			})
		}
	}

	err := eg.Wait()
	close(distroCh)

	if err != nil {
		return nil, fmt.Errorf("reconcile failed: %w", err)
	}

	return distros, nil
}

func (m *Manager) reconcileForArch(name string, arch string, downloader downloader) (*Distro, error) {
	m.logger.Debug("checking whether distro needs reconciling",
		"distro", name,
		"arch", arch,
	)

	directory := filepath.Join(m.storageDirectory, name, arch)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create directories in path '%s': %w", directory, err)
	}

	metaFilePath := filepath.Join(directory, metadataFilename)
	metaFileExists := false

	if stat, err := os.Stat(metaFilePath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("metadata file %s stat failed: %w", metaFilePath, err)
	} else if err == nil && stat.Size() > 0 {
		metaFileExists = true
	}

	metaFile, err := os.OpenFile(metaFilePath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("failed to open metadata: %w", err)
	}
	defer metaFile.Close()

	if metaFileExists {
		var meta metadata
		if err := json.NewDecoder(metaFile).Decode(&meta); err != nil {
			// TODO: could proceed on here and redownload, assuming that it's corrupted?
			return nil, fmt.Errorf("could not parse distro metadata: %w", err)
		}

		drifted, err := downloader.HasDrifted(&meta)
		if err != nil {
			return nil, fmt.Errorf("failed to check distro drift: %w", err)
		}

		// Distro hasn't drifted! We can stop here
		if !drifted {
			m.logger.Info("distro is up-to-date and not drifted from desired state",
				"distro", name,
				"arch", arch,
			)

			distro, err := meta.distro(directory, arch)
			if err != nil {
				return nil, fmt.Errorf("could not get existing distro pointed to by metadata: %w", err)
			}

			return distro, nil
		}
	}

	// Either distro has drifted, or we don't have any metadata. Reconcile by downloading!
	m.logger.Info("distro has drifted and will be reconciled",
		"distro", name,
		"arch", arch,
	)

	dataDirectory := filepath.Join(directory, downloader.Hash())
	if err := os.MkdirAll(dataDirectory, 0o700); err != nil {
		return nil, fmt.Errorf("failed to create directories in path '%s': %w", dataDirectory, err)
	}

	meta, err := downloader.Download(dataDirectory)
	if err != nil {
		return nil, fmt.Errorf("download of distro failed: %w", err)
	}

	if err := metaFile.Truncate(0); err != nil {
		return nil, fmt.Errorf("failed to truncate metadata file: %w", err)
	}

	if _, err := metaFile.Seek(0, io.SeekStart); err != nil {
		return nil, fmt.Errorf("failed to seek metadata file: %w", err)
	}

	if err := json.NewEncoder(metaFile).Encode(meta); err != nil {
		return nil, fmt.Errorf("failed to write metadata for distro: %w", err)
	}

	m.logger.Info("distro has been reconciled",
		"distro", name,
		"arch", arch,
	)

	distro, err := meta.distro(directory, arch)
	if err != nil {
		return nil, fmt.Errorf("could not create distro after reconciliation: %w", err)
	}

	return distro, nil
}

func (m *metadata) distro(directory string, arch string) (*Distro, error) {
	// Ensure hash isn't doing path traversal
	versionDirectory := filepath.Clean(filepath.Join(directory, m.Hash))
	if _, err := filepath.Rel(directory, versionDirectory); err != nil {
		return nil, errCorruptedMetadata
	}

	initrdPath := filepath.Clean(filepath.Join(versionDirectory, m.InitrdPath))
	if _, err := filepath.Rel(versionDirectory, initrdPath); err != nil {
		return nil, errCorruptedMetadata
	}

	kernelPath := filepath.Clean(filepath.Join(versionDirectory, m.KernelPath))
	if _, err := filepath.Rel(versionDirectory, kernelPath); err != nil {
		return nil, errCorruptedMetadata
	}

	return &Distro{
		kernelPath: kernelPath,
		initrdPath: initrdPath,
		arch:       arch,
	}, nil
}
