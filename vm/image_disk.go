package vm

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	v1 "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func BuildImageExt4Disk(ctx context.Context, imageRef, diskPath string, sizeMiB int, label string) error {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}

	platform, err := guestPlatform()
	if err != nil {
		return err
	}

	img, err := remote.Image(ref, remote.WithContext(ctx), remote.WithPlatform(platform))
	if err != nil {
		return fmt.Errorf("pull image %q: %w", imageRef, err)
	}

	return writeImageToDisk(ctx, img, imageRef, diskPath, sizeMiB, label)
}

func writeImageToDisk(ctx context.Context, img v1.Image, imageRef, diskPath string, sizeMiB int, label string) error {
	stagingDir, err := os.MkdirTemp("", "vmrunner-image-disk-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}

	defer os.RemoveAll(stagingDir)

	rootfsDir := filepath.Join(stagingDir, "images", sanitizeImageRef(imageRef), "rootfs")
	if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
		return fmt.Errorf("create rootfs staging dir: %w", err)
	}

	rc := mutate.Extract(img)
	defer rc.Close()

	if err := extractTarInto(rootfsDir, rc); err != nil {
		return fmt.Errorf("populate rootfs staging dir: %w", err)
	}

	if err := CreateExt4DiskFromDir(ctx, diskPath, sizeMiB, label, stagingDir); err != nil {
		return fmt.Errorf("create populated ext4 disk: %w", err)
	}

	return nil
}

func PrepareImageExt4Disk(ctx context.Context, imageRef, cacheDir string, sizeMiB int, label string) (string, bool, error) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return "", false, fmt.Errorf("create image cache dir %q: %w", cacheDir, err)
	}

	platform, err := guestPlatform()
	if err != nil {
		return "", false, err
	}

	// Fast path: previously resolved ref->digest mapping.
	refCacheDir := filepath.Join(cacheDir, "refs")
	_ = os.MkdirAll(refCacheDir, 0o755)
	refKey := sanitizeImageRef(imageRef) + "." + platform.Architecture
	refCachePath := filepath.Join(refCacheDir, refKey+".digest")
	if b, err := os.ReadFile(refCachePath); err == nil {
		cachedHex := strings.TrimSpace(string(b))
		if cachedHex != "" {
			cachedDiskPath := filepath.Join(cacheDir, cachedHex+".raw")
			if _, statErr := os.Stat(cachedDiskPath); statErr == nil {
				return cachedDiskPath, false, nil
			}
		}
	}

	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", false, fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}

	desc, err := remote.Get(ref, remote.WithContext(ctx), remote.WithPlatform(platform))
	if err != nil {
		return "", false, fmt.Errorf("resolve manifest digest for %q: %w", imageRef, err)
	}

	digestHex := strings.TrimPrefix(desc.Digest.String(), "sha256:")
	if digestHex == "" {
		return "", false, fmt.Errorf("empty manifest digest for %q", imageRef)
	}

	diskPath := filepath.Join(cacheDir, digestHex+".raw")
	if _, err := os.Stat(diskPath); err == nil {
		_ = os.WriteFile(refCachePath, []byte(digestHex+"\n"), 0o644)
		return diskPath, false, nil
	}

	img, err := desc.Image()
	if err != nil {
		return "", false, fmt.Errorf("load image descriptor for %q: %w", imageRef, err)
	}

	if err := writeImageToDisk(ctx, img, imageRef, diskPath, sizeMiB, label); err != nil {
		return "", false, fmt.Errorf("build image disk: %w", err)
	}

	_ = os.WriteFile(refCachePath, []byte(digestHex+"\n"), 0o644)
	return diskPath, true, nil
}

func guestPlatform() (v1.Platform, error) {
	switch runtime.GOARCH {
	case "arm64":
		return v1.Platform{OS: "linux", Architecture: "arm64"}, nil
	case "amd64":
		return v1.Platform{OS: "linux", Architecture: "amd64"}, nil
	default:
		return v1.Platform{}, fmt.Errorf("unsupported host arch for guest platform selection: %s", runtime.GOARCH)
	}
}

func sanitizeImageRef(imageRef string) string {
	replacer := strings.NewReplacer("/", "_", ":", "_", "@", "_")
	return replacer.Replace(imageRef)
}

func extractTarInto(root string, r io.Reader) error {
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		target, err := safeJoin(root, h.Name)
		if err != nil {
			continue
		}

		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(h.Mode)); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(h.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				_ = f.Close()
				return err
			}
			if err := f.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Symlink(h.Linkname, target); err != nil {
				return err
			}
		case tar.TypeLink:
			linkTarget, err := safeJoin(root, h.Linkname)
			if err != nil {
				continue
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			_ = os.Remove(target)
			if err := os.Link(linkTarget, target); err != nil {
				return err
			}
		default:
		}
	}
}

func safeJoin(root, name string) (string, error) {
	clean := filepath.Clean(name)
	if clean == "." || clean == string(filepath.Separator) {
		return "", fmt.Errorf("invalid archive path %q", name)
	}

	for strings.HasPrefix(clean, string(filepath.Separator)) {
		clean = strings.TrimPrefix(clean, string(filepath.Separator))
	}

	full := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive entry escapes root: %q", name)
	}
	return full, nil
}
