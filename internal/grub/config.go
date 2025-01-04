package grub

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"text/template"

	"github.com/davejbax/pixie/internal/efipe"
)

const kernelImageName = "kernel.img"

type Config struct {
	Root    string   `default:"/usr/lib/grub/{{ .Arch }}-efi"`
	Modules []string `default:"[\"normal\", \"tftp\", \"http\", \"linux\", \"fat\", \"iso9660\"]"`
}

type rootTemplateOptions struct {
	Arch string
}

// TODO: definitely split up this function
func NewImageFromConfig(config *Config, arch string, prefix string) (*Image, func(), error) {
	rootBuff := &bytes.Buffer{}
	rootTmpl, err := template.New("root").Parse(config.Root)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse GRUB root path template: %w", err)
	}

	if err := rootTmpl.Execute(rootBuff, &rootTemplateOptions{
		Arch: arch,
	}); err != nil {
		return nil, nil, fmt.Errorf("failed to execute GRUB root path template: %w", err)
	}

	root := rootBuff.String()

	moddepFile, err := os.Open(filepath.Join(root, "moddep.lst"))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open GRUB moddep.lst file: %w", err)
	}
	defer moddepFile.Close()

	moddep, err := NewDependencyList(moddepFile)
	if err != nil {
		return nil, nil, fmt.Errorf("could not read GRUB moddep.lst: %w", err)
	}

	modulesWithDependencies, err := moddep.Resolve(config.Modules)
	if err != nil {
		return nil, nil, fmt.Errorf("could not resolve module dependencies: %w", err)
	}

	modules := make([]*Module, 0, len(modulesWithDependencies)+1)

	for _, moduleName := range modulesWithDependencies {
		module, err := NewModuleFromDirectory(root, moduleName)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load module '%s' from root %s: %w", moduleName, root, err)
		}

		modules = append(modules, module)
	}

	modules = append(modules, NewPrefixModule(prefix))

	kernel, err := os.Open(filepath.Join(root, kernelImageName))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open GRUB kernel for arch '%s': %w", arch, err)
	}

	img, err := NewImage(kernel, modules, efipe.UEFIPageSize)
	if err != nil {
		_ = kernel.Close()
		return nil, nil, fmt.Errorf("failed to create GRUB image: %w", err)
	}

	return img, func() { _ = kernel.Close() }, nil
}
