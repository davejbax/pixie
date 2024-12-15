package bootloader

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/mholt/archiver/v4"
)

type DownloadStatusError struct {
	StatusCode int
	URL        string
	Body       string
}

func (d *DownloadStatusError) Error() string {
	return fmt.Sprintf("download from '%s' gave error '%d' with body '%s'", d.URL, d.StatusCode, d.Body)
}

type DownloadOptions struct {
	Version string
}

func downloadURL(tmplText string, options *DownloadOptions) (string, error) {
	tmpl, err := template.New("downloadURL").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("could not parse download URL template: %w", err)
	}

	var buff bytes.Buffer
	if err := tmpl.Execute(&buff, options); err != nil {
		return "", fmt.Errorf("failed to execute download URL template: %w", err)
	}

	return buff.String(), nil
}

func download(ctx context.Context, dst io.Writer, url string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to form request for URL '%s': %w", url, err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to download '%s': %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			body = []byte(fmt.Sprintf("<reading error: %v>", err))
		}

		return &DownloadStatusError{
			StatusCode: resp.StatusCode,
			URL:        url,
			Body:       string(body),
		}
	}

	_, err = io.Copy(dst, resp.Body)
	if err != nil {
		return fmt.Errorf("error writing body to destination: %w", err)
	}

	return nil
}

func extractor(ctx context.Context, archive io.Reader, filename string, destDirectory string, stripTopLevel bool) func() error {
	return func() error {
		format, archive, err := archiver.Identify(ctx, filename, archive)
		if err != nil {
			return fmt.Errorf("failed to identify archive type: %w", err)
		}

		ex, ok := format.(archiver.Extractor)
		if !ok {
			return fmt.Errorf("failed to get extractor for archive %s: %w", filename, ErrCannotExtractArchiveType)
		}

		return ex.Extract(ctx, archive, func(ctx context.Context, info archiver.FileInfo) error {
			name := path.Clean(info.NameInArchive)
			if !filepath.IsLocal(name) {
				return ErrInsecurePath
			}

			if stripTopLevel {
				if _, after, found := strings.Cut(name, "/"); found {
					name = after
				}
			}

			destPath := filepath.Join(destDirectory, name)

			if info.IsDir() {
				return os.Mkdir(destPath, info.Mode())
			}

			if !info.Mode().IsRegular() {
				return &UnsupportedFileError{mode: info.Mode(), name: name}
			}

			// Regular file
			src, err := info.Open()
			if err != nil {
				return fmt.Errorf("failed to open file '%s': %w", name, err)
			}
			defer src.Close()

			dest, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY, info.Mode().Perm())
			if err != nil {
				return fmt.Errorf("failed to open output file '%s': %w", destPath, err)
			}

			if _, err := io.Copy(dest, src); err != nil {
				return fmt.Errorf("failed to write file from archive '%s': %w", destPath, err)
			}

			return nil
		})
	}
}
