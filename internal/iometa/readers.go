package iometa

import (
	"errors"
	"fmt"
	"io"
	"time"
)

var errInvalidWhence = errors.New("invalid whence argument")

type Closifier struct {
	io.Reader
}

func (*Closifier) Close() error {
	return nil
}

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

type ProgressReader struct {
	bytesRead     int64
	bytesExpected int64

	callback   func(progress float64, read int64, expected int64)
	cadence    time.Duration
	lastUpdate *time.Time
}

func (w *ProgressReader) Read(b []byte) (int, error) {
	w.bytesRead += int64(len(b))

	if w.lastUpdate == nil || time.Since(*w.lastUpdate) >= w.cadence {
		w.callback(float64(w.bytesRead)/float64(w.bytesExpected), w.bytesRead, w.bytesExpected)
	}

	return len(b), nil
}
