package tftp

import (
	"io"

	"github.com/davejbax/pixie/internal/bootloader"
	pintftp "github.com/pin/tftp/v3"
)

type Server struct {
	tftp       *pintftp.Server
	bootloader bootloader.Bootloader
}

func NewServer(bl bootloader.Bootloader) *Server {
	srv := &Server{
		bootloader: bl,
	}

	srv.tftp = pintftp.NewServer(srv.handleRead, nil)
}

func (*Server) handleRead(filename string, rf io.ReaderFrom) error {
}
