package bootloader

import "io"

type Bootloader interface {
	Entrypoint() io.Reader
}
