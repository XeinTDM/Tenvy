package plugins

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func unpackZipArchive(path, dest, entryPoint string, secret []byte) error {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return fmt.Errorf("open artifact archive: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if err := extractZipEntry(file, dest, entryPoint, secret); err != nil {
			return err
		}
	}
	return nil
}

func extractZipEntry(entry *zip.File, dest, entryPoint string, secret []byte) error {
	cleaned := filepath.Clean(entry.Name)
	if cleaned == "." || cleaned == "" {
		return nil
	}
	target := filepath.Join(dest, cleaned)
	if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
		return fmt.Errorf("artifact entry escapes destination: %s", entry.Name)
	}

	if entry.FileInfo().IsDir() {
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("create artifact directory: %w", err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return fmt.Errorf("prepare artifact path: %w", err)
	}

	reader, err := entry.Open()
	if err != nil {
		return fmt.Errorf("open artifact entry: %w", err)
	}
	defer reader.Close()

	shouldEncrypt := len(secret) > 0 && (cleaned == entryPoint || cleaned == "manifest.json")
	var data []byte
	
	if shouldEncrypt {
		// Read fully into memory to encrypt
		var buf bytes.Buffer
		if _, err := io.Copy(&buf, reader); err != nil {
			return fmt.Errorf("read artifact entry: %w", err)
		}
		encrypted, err := encrypt(buf.Bytes(), secret)
		if err != nil {
			return fmt.Errorf("encrypt artifact entry: %w", err)
		}
		data = encrypted
		target += ".enc"
	}

	temp, err := os.CreateTemp(filepath.Dir(target), "entry-*.tmp")
	if err != nil {
		return fmt.Errorf("create artifact temp file: %w", err)
	}
	tempPath := temp.Name()
	
	if shouldEncrypt {
		if _, err := temp.Write(data); err != nil {
			temp.Close()
			os.Remove(tempPath)
			return fmt.Errorf("write encrypted entry: %w", err)
		}
	} else {
		if _, err := io.Copy(temp, reader); err != nil {
			temp.Close()
			os.Remove(tempPath)
			return fmt.Errorf("write artifact entry: %w", err)
		}
	}

	if err := temp.Close(); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("close artifact entry: %w", err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		os.Remove(tempPath)
		return fmt.Errorf("finalize artifact entry: %w", err)
	}
	if mode := entry.Mode(); mode != 0 {
		os.Chmod(target, mode)
	}
	return nil
}

func unpackTarGzArchive(path, dest, entryPoint string, secret []byte) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open artifact archive: %w", err)
	}
	defer file.Close()

	gz, err := gzip.NewReader(file)
	if err != nil {
		return fmt.Errorf("open artifact archive: %w", err)
	}
	defer gz.Close()

	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read artifact archive: %w", err)
		}
		if err := extractTarEntry(header, reader, dest, entryPoint, secret); err != nil {
			return err
		}
	}
	return nil
}

func extractTarEntry(header *tar.Header, reader io.Reader, dest, entryPoint string, secret []byte) error {
	cleaned := filepath.Clean(header.Name)
	if cleaned == "." || cleaned == "" {
		return nil
	}
	target := filepath.Join(dest, cleaned)
	if !strings.HasPrefix(target, dest+string(os.PathSeparator)) && target != dest {
		return fmt.Errorf("artifact entry escapes destination: %s", header.Name)
	}

	switch header.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(target, 0o755); err != nil {
			return fmt.Errorf("create artifact directory: %w", err)
		}
		return nil
	case tar.TypeReg, tar.TypeRegA:
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("prepare artifact path: %w", err)
		}

		shouldEncrypt := len(secret) > 0 && (cleaned == entryPoint || cleaned == "manifest.json")
		var data []byte

		if shouldEncrypt {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, reader); err != nil {
				return fmt.Errorf("read artifact entry: %w", err)
			}
			encrypted, err := encrypt(buf.Bytes(), secret)
			if err != nil {
				return fmt.Errorf("encrypt artifact entry: %w", err)
			}
			data = encrypted
			target += ".enc"
		}

		temp, err := os.CreateTemp(filepath.Dir(target), "entry-*.tmp")
		if err != nil {
			return fmt.Errorf("create artifact temp file: %w", err)
		}
		tempPath := temp.Name()

		if shouldEncrypt {
			if _, err := temp.Write(data); err != nil {
				temp.Close()
				os.Remove(tempPath)
				return fmt.Errorf("write encrypted entry: %w", err)
			}
		} else {
			if _, err := io.Copy(temp, reader); err != nil {
				temp.Close()
				os.Remove(tempPath)
				return fmt.Errorf("write artifact entry: %w", err)
			}
		}

		if err := temp.Close(); err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("close artifact entry: %w", err)
		}
		if err := os.Rename(tempPath, target); err != nil {
			os.Remove(tempPath)
			return fmt.Errorf("finalize artifact entry: %w", err)
		}
		if mode := header.FileInfo().Mode(); mode != 0 {
			os.Chmod(target, mode)
		}
		return nil
	case tar.TypeXHeader, tar.TypeXGlobalHeader, tar.TypeGNUSparse, tar.TypeGNULongName, tar.TypeGNULongLink:
		return nil
	default:
		return fmt.Errorf("artifact entry type not supported: %s", header.Name)
	}
}
