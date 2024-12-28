package iometa

import "io"

type CountingWriter struct {
	Writer       io.Writer
	bytesWritten int
}

func (c *CountingWriter) Write(p []byte) (int, error) {
	written, err := c.Writer.Write(p)
	c.bytesWritten += written

	return written, err
}

func (c *CountingWriter) BytesWritten() int {
	return c.bytesWritten
}
