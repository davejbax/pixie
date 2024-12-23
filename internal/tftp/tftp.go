package tftp

import (
	"errors"
	"io"

	"github.com/davejbax/pixie/internal/bootloader"
	pintftp "github.com/pin/tftp/v3"
)

type Server struct {
	tftp        *pintftp.Server
	bootloaders map[string]bootloader.Bootloader
}

var errFileNotFound = errors.New("requested file not found")

func NewServer(bootloaders []bootloader.Bootloader) *Server {
	// TODO: tell user off if they give no bootloaders here
	bootloadersByEntrypointPath := make(map[string]bootloader.Bootloader)
	for _, bl := range bootloaders {
		bootloadersByEntrypointPath[bl.EntrypointPath()] = bl
	}

	srv := &Server{
		bootloaders: bootloadersByEntrypointPath,
	}

	srv.tftp = pintftp.NewServer(srv.handleRead, nil)

	return srv
}

func (s *Server) handleRead(filename string, rf io.ReaderFrom) error {
	bl, ok := s.bootloaders[filename]
	if !ok {
		return errFileNotFound
	}

	_, err := rf.ReadFrom(bl.Entrypoint())
	return err
}
