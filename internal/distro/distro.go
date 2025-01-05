package distro

import "os"

type Distro struct {
	kernelPath string
	initrdPath string
	arch       string
}

func (d *Distro) Kernel() (*os.File, error) {
	return os.Open(d.kernelPath) //nolint:wrapcheck
}

func (d *Distro) Initrd() (*os.File, error) {
	return os.Open(d.initrdPath) //nolint:wrapcheck
}
