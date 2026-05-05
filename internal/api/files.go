package api

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
)

type FileInfo struct {
	Name         string  `json:"name"`
	Size         float64 `json:"size" doc:"Size in megabytes"`
	Modified     int64   `json:"modified" doc:"Unix timestamp"`
	HasThumbnail bool    `json:"has_thumbnail"`
	Location     string  `json:"location" doc:"'temp' or 'output'"`
}

func ListFiles(dir, location string) ([]FileInfo, error) {
	files := []FileInfo{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return files, nil
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		if !strings.HasSuffix(strings.ToLower(filename), ".mp3") {
			continue
		}
		full := filepath.Join(dir, filename)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		fileSizeMB := float64(info.Size()) / (1024 * 1024)
		fileSize := float64(int(fileSizeMB*100+0.5)) / 100

		files = append(files, FileInfo{
			Name:         filename,
			Size:         fileSize,
			Modified:     info.ModTime().Unix(),
			HasThumbnail: CheckMP3HasArtwork(full),
			Location:     location,
		})
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Modified > files[j].Modified })
	return files, nil
}

// MoveFiles moves files from tempDir to outputDir, falling back to copy+delete on EXDEV.
// Returns the count moved and a per-file error list.
func MoveFiles(tempDir, outputDir string, filenames []string) (int, []string) {
	moved := 0
	errs := []string{}

	for _, filename := range filenames {
		src := filepath.Join(tempDir, filename)
		dst := filepath.Join(outputDir, filename)

		if !strings.HasPrefix(src, tempDir) || !strings.HasPrefix(dst, outputDir) {
			errs = append(errs, fmt.Sprintf("%s: Invalid path", filename))
			continue
		}
		if _, err := os.Stat(src); os.IsNotExist(err) {
			errs = append(errs, fmt.Sprintf("%s: File not found in temp", filename))
			continue
		}

		err := os.Rename(src, dst)
		if err != nil {
			if linkErr, ok := err.(*os.LinkError); ok && linkErr.Err == syscall.EXDEV {
				if copyErr := copyFile(src, dst); copyErr != nil {
					errs = append(errs, fmt.Sprintf("%s: %v", filename, copyErr))
					continue
				}
				if removeErr := os.Remove(src); removeErr != nil {
					errs = append(errs, fmt.Sprintf("%s: copied but failed to remove source: %v", filename, removeErr))
				}
			} else {
				errs = append(errs, fmt.Sprintf("%s: %v", filename, err))
				continue
			}
		}
		moved++
	}
	return moved, errs
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// SafeJoin joins baseDir and relPath, returning an error if the result escapes baseDir.
func SafeJoin(baseDir, relPath string) (string, error) {
	relPath = strings.TrimPrefix(relPath, "/")
	full := filepath.Join(baseDir, relPath)
	if !strings.HasPrefix(full, baseDir) {
		return "", fmt.Errorf("path escapes base directory")
	}
	return full, nil
}
