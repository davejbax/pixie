package main

import (
	"fmt"

	"github.com/creasty/defaults"
	"github.com/davejbax/pixie/internal/grub"
	"github.com/spf13/viper"
)

type config struct {
	TempDir string `mapstructure:"temp_dir"`

	Grub grub.Config
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
