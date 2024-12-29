package iometa

import (
	"fmt"
	"io"
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
