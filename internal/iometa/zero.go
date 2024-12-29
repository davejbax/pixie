package iometa

import (
	"errors"
	"fmt"
	"io"
)

var errInvalidWhence = errors.New("invalid whence argument")

type ZeroReader struct {
	Size int

	offset int
}

func (r *ZeroReader) Read(buff []byte) (int, error) {
	bytesToWrite := min(len(buff), r.Size-r.offset)

	for i := 0; i < bytesToWrite; i++ {
		buff[i] = 0
	}

	r.offset += bytesToWrite

	if r.offset == r.Size {
		return bytesToWrite, io.EOF
	}

	return bytesToWrite, nil
}

func (r *ZeroReader) Seek(offset int64, whence int) (int64, error) {
	switch whence {
	case io.SeekCurrent:
		r.offset += int(offset)
	case io.SeekEnd:
		r.offset = r.Size
	case io.SeekStart:
		r.offset = int(offset)
	default:
		return -1, errInvalidWhence
	}

	return int64(r.offset), nil
}

func WriteZeros(w io.Writer, count int) error {
	r := &ZeroReader{Size: count}
	if _, err := io.Copy(w, r); err != nil {
		return fmt.Errorf("failed to write zeros: %w", err)
	}

	return nil
}
