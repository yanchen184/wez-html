package archive

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	MaxFileSize  int64 = 50 * 1024 * 1024  // 50 MB
	MaxTotalSize int64 = 500 * 1024 * 1024 // 500 MB
	MaxFiles           = 10000
)

var denyExt = map[string]bool{
	".exe": true, ".sh": true, ".py": true, ".php": true,
	".jar": true, ".bat": true, ".cmd": true, ".dll": true,
	".so": true, ".dylib": true,
}

type Stats struct {
	Files     int
	SizeBytes int64
}

func Pack(srcDir string, w io.Writer) (Stats, error) {
	var st Stats
	gz := gzip.NewWriter(w)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()

	err := filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		base := filepath.Base(rel)
		if base == ".git" || base == "node_modules" || base == "__pycache__" || base == ".DS_Store" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.ToSlash(rel)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			n, err := io.Copy(tw, f)
			f.Close()
			if err != nil {
				return err
			}
			st.Files++
			st.SizeBytes += n
		}
		return nil
	})
	if err != nil {
		return st, err
	}
	return st, nil
}

func Unpack(r io.Reader, destDir string) (Stats, error) {
	var st Stats
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return st, err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return st, fmt.Errorf("gzip: %w", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return st, fmt.Errorf("tar: %w", err)
		}
		if err := validateName(hdr.Name); err != nil {
			return st, err
		}
		ext := strings.ToLower(filepath.Ext(hdr.Name))
		if denyExt[ext] {
			return st, fmt.Errorf("deny ext: %s", hdr.Name)
		}
		dst := filepath.Join(destDir, filepath.FromSlash(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return st, err
			}
		case tar.TypeReg, tar.TypeRegA:
			if hdr.Size > MaxFileSize {
				return st, fmt.Errorf("file too big (%d > %d): %s", hdr.Size, MaxFileSize, hdr.Name)
			}
			if st.SizeBytes+hdr.Size > MaxTotalSize {
				return st, fmt.Errorf("total too big (> %d)", MaxTotalSize)
			}
			if st.Files+1 > MaxFiles {
				return st, fmt.Errorf("too many files (> %d)", MaxFiles)
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return st, err
			}
			f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return st, err
			}
			n, err := io.CopyN(f, tr, hdr.Size)
			f.Close()
			if err != nil && err != io.EOF {
				return st, err
			}
			st.Files++
			st.SizeBytes += n
		default:
			// skip symlinks etc
		}
	}
	return st, nil
}

func validateName(name string) error {
	clean := filepath.ToSlash(filepath.Clean(name))
	if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || strings.Contains(clean, "/../") {
		return errors.New("unsafe path: " + name)
	}
	if strings.Contains(name, "\x00") {
		return errors.New("null byte in path")
	}
	return nil
}
