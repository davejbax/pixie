package pe

import "io"

type PEImage struct {
}

func (p *PEImage) WriteTo(w io.Writer) (int64, error) {
	return 0, nil
}
