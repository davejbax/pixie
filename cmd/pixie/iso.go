package main

import (
	"debug/pe"
	"fmt"
	"os"

	"github.com/davejbax/pixie/internal/efipe"
	"github.com/davejbax/pixie/internal/grub"
	"github.com/davejbax/pixie/internal/iso"
	"github.com/spf13/cobra"
)

func newISOCommand(opts *rootOptions) *cobra.Command {
	outputPath := ""

	cmd := &cobra.Command{
		Use:   "iso",
		Short: "Generate bootable ISO images",
		RunE: func(_ *cobra.Command, _ []string) error {
			grubImage, cleanup, err := grub.NewImageFromConfig(&opts.config.Grub, "x86_64", "(cd0)")
			if err != nil {
				return fmt.Errorf("failed to create GRUB image from config: %w", err)
			}
			defer cleanup()

			efi, err := efipe.New(grubImage, grubImage.PEHeaderSize())
			if err != nil {
				return fmt.Errorf("failed to create EFI PE image: %w", err)
			}

			output, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
			if err != nil {
				return fmt.Errorf("could not open output ISO file: %w", err)
			}

			builder := iso.NewBuilder(opts.config.TempDir)

			if err := builder.AddEFIEntrypoint(efi, pe.IMAGE_FILE_MACHINE_AMD64); err != nil {
				return fmt.Errorf("failed to add EFI entrypoint: %w", err)
			}

			if err := builder.Build(output); err != nil {
				return fmt.Errorf("ISO build failed: %w", err)
			}

			opts.logger.Info("successfully created ISO image",
				"path", outputPath,
			)

			return nil
		},
	}

	cmd.Flags().StringVarP(&outputPath, "output", "o", "pixie.iso", "Path to output ISO file")

	return cmd
}
