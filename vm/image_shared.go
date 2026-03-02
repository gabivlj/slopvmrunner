package vm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1/mutate"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

func PrepareSharedContainerRootFS(ctx context.Context, imageRef, sharedHostDir string) (string, bool, error) {
	ref, err := name.ParseReference(imageRef)
	if err != nil {
		return "", false, fmt.Errorf("parse image reference %q: %w", imageRef, err)
	}
	platform, err := guestPlatform()
	if err != nil {
		return "", false, err
	}
	desc, err := remote.Get(ref, remote.WithContext(ctx), remote.WithPlatform(platform))
	if err != nil {
		return "", false, fmt.Errorf("resolve manifest digest for %q: %w", imageRef, err)
	}
	digestHex := strings.TrimPrefix(desc.Digest.String(), "sha256:")
	if digestHex == "" {
		return "", false, fmt.Errorf("empty manifest digest for %q", imageRef)
	}

	rootfsDir := filepath.Join(sharedHostDir, digestHex, "rootfs")
	if _, err := os.Stat(rootfsDir); err == nil {
		return digestHex, false, nil
	} else if err != nil {
		if !os.IsNotExist(err) {
			return "", false, err
		}
		img, err := desc.Image()
		if err != nil {
			return "", false, fmt.Errorf("load image descriptor for %q: %w", imageRef, err)
		}
		if err := os.MkdirAll(rootfsDir, 0o755); err != nil {
			return "", false, err
		}
		rc := mutate.Extract(img)
		defer rc.Close()
		if err := extractTarInto(rootfsDir, rc); err != nil {
			return "", false, fmt.Errorf("extract image to shared rootfs: %w", err)
		}
		return digestHex, true, nil
	}
	return digestHex, false, nil
}
