package main

import (
	"fmt"

	"github.com/creasty/defaults"
	"github.com/davejbax/pixie/internal/distro"
	"github.com/davejbax/pixie/internal/grub"
	"github.com/spf13/viper"
)

type config struct {
	TempDir    string `mapstructure:"temp_directory" default:"/var/tmp/pixie"`
	StorageDir string `mapstructure:"storage_directory" default:"/var/lib/pixie"`

	Grub grub.Config

	Distros map[string]*distro.Config
}

func loadConfig(path string) (*config, error) {
	viper.SetConfigFile(path)
	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config from '%s': %w", path, err)
	}

	config := &config{}

	if err := defaults.Set(config); err != nil {
		return nil, fmt.Errorf("failed to set config defaults: %w", err)
	}

	if err := viper.Unmarshal(config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	return config, nil
}
