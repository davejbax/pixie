package main

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

const defaultConfigPath = "/etc/pixie/config.yaml"

func main() {
	root := newRootCommand()
	if err := root.Execute(); err != nil {
		log.Fatal(err)
	}
}

type rootOptions struct {
	logger *slog.Logger
	config *config
}

func newRootCommand() *cobra.Command {
	opts := &rootOptions{}

	level := logLevelFlag{Level: slog.LevelWarn}
	format := logHandlerFlagText
	configPath := ""

	cmd := &cobra.Command{
		Use:           "pixie",
		Short:         "Pixie is a PXE boot server with declarative configuration",
		SilenceErrors: false,
		SilenceUsage:  true,
		PersistentPreRunE: func(_ *cobra.Command, _ []string) error {
			opts.logger = slog.New(format.CreateHandler(level.Level))

			var err error
			opts.config, err = loadConfig(configPath)
			if err != nil {
				return err
			}

			return nil
		},
	}

	cmd.PersistentFlags().Var(&level, "level", "Log output level")
	cmd.PersistentFlags().Var(&format, "format", "Log output format")
	cmd.PersistentFlags().StringVar(&configPath, "config", defaultConfigPath, "Path to config file to use")

	cmd.AddCommand(newISOCommand(opts))

	return cmd
}

type logLevelFlag struct {
	slog.Level
}

func (l *logLevelFlag) Set(name string) error {
	if err := l.UnmarshalText([]byte(name)); err != nil {
		return fmt.Errorf("failed to parse level: %w", err)
	}

	return nil
}

func (l *logLevelFlag) Type() string {
	return "<DEBUG|INFO|WARN|ERROR>"
}

type logHandlerFlag string

const (
	logHandlerFlagText = logHandlerFlag("text")
	logHandlerFlagJSON = logHandlerFlag("json")
)

var errUnrecognisedLogHandler = errors.New("invalid log format; valid values are 'text' or 'json'")

func (l *logHandlerFlag) Set(name string) error {
	if name != string(logHandlerFlagJSON) && name != string(logHandlerFlagText) {
		return errUnrecognisedLogHandler
	}

	*l = logHandlerFlag(name)
	return nil
}

func (l *logHandlerFlag) String() string {
	return string(*l)
}

func (l *logHandlerFlag) Type() string {
	return "<" + strings.Join([]string{string(logHandlerFlagText), string(logHandlerFlagJSON)}, "|") + ">"
}

func (l *logHandlerFlag) CreateHandler(level slog.Level) slog.Handler {
	switch *l {
	case logHandlerFlagText:
		return slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	case logHandlerFlagJSON:
		return slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: level})
	default:
		panic("invalid handler value")
	}
}
