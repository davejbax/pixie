package iometa

import (
	"fmt"
	"io"
	"time"
)

type CountingWriter struct {
	Writer       io.Writer
	bytesWritten int
}

func (c *CountingWriter) Write(p []byte) (int, error) {
	written, err := c.Writer.Write(p)
	c.bytesWritten += written

	// Wrap the error to let the caller know that it wasn't us that failed!
	if err != nil {
		return written, fmt.Errorf("wrapped write failed: %w", err)
	}

	return written, nil
}

func (c *CountingWriter) BytesWritten() int {
	return c.bytesWritten
}

type ProgressWriter struct {
	bytesWritten  int64
	bytesExpected int64

	callback   func(progress float64, written int64, expected int64)
	cadence    time.Duration
	lastUpdate time.Time
}

func NewProgressWriter(callback func(progress float64, written int64, expected int64), cadence time.Duration, bytesExpected int64) *ProgressWriter {
	return &ProgressWriter{
		callback:      callback,
		cadence:       cadence,
		bytesExpected: bytesExpected,
		lastUpdate:    time.Now(),
	}
}

func (w *ProgressWriter) Write(b []byte) (int, error) {
	w.bytesWritten += int64(len(b))

	if time.Since(w.lastUpdate) >= w.cadence {
		progress := float64(0)
		if w.bytesExpected > 0 {
			progress = float64(w.bytesWritten) / float64(w.bytesExpected)
		}

		w.callback(progress, w.bytesWritten, w.bytesExpected)
		w.lastUpdate = time.Now()
	}

	return len(b), nil
}
