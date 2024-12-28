package iometa

import "io"

type Closifier struct {
	io.Reader
}

func (*Closifier) Close() error {
	return nil
}
