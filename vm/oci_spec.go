package vm

import "encoding/json"

type DefaultOCISpecOptions struct {
	ImageRef      string
	RootfsPath    string
	Entrypoint    []string
	Cmd           []string
	Terminal      bool
	ReadonlyRoot  bool
	ContainerName string
}

func BuildDefaultOCISpecJSON(opts DefaultOCISpecOptions) ([]byte, error) {
	rootfsPath := opts.RootfsPath
	if rootfsPath == "" {
		rootfsPath = "/"
	}
	entrypoint := opts.Entrypoint
	if len(entrypoint) == 0 {
		entrypoint = []string{"/bin/sh", "-lc"}
	}
	cmd := opts.Cmd
	if len(cmd) == 0 {
		cmd = []string{"echo hello world; sleep infinity"}
	}
	processArgs := append(append([]string{}, entrypoint...), cmd...)
	imageRef := opts.ImageRef
	if imageRef == "" {
		imageRef = "unknown"
	}

	spec := map[string]any{
		"ociVersion": "1.1.0",
		"process": map[string]any{
			"terminal": opts.Terminal,
			"user": map[string]any{
				"uid": 0,
				"gid": 0,
			},
			"args": processArgs,
			"env": []string{
				"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
				"VMRUNNER_IMAGE_REF=" + imageRef,
			},
			"cwd": "/",
		},
		"root": map[string]any{
			"path":     rootfsPath,
			"readonly": opts.ReadonlyRoot,
		},
		"mounts": []map[string]any{
			{
				"destination": "/proc",
				"type":        "proc",
				"source":      "proc",
				"options":     []string{"nosuid", "noexec", "nodev"},
			},
			{
				"destination": "/dev",
				"type":        "tmpfs",
				"source":      "tmpfs",
				"options":     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
			},
			{
				"destination": "/dev/pts",
				"type":        "devpts",
				"source":      "devpts",
				"options":     []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620", "gid=5"},
			},
			{
				"destination": "/dev/shm",
				"type":        "tmpfs",
				"source":      "shm",
				"options":     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"},
			},
			{
				"destination": "/dev/mqueue",
				"type":        "mqueue",
				"source":      "mqueue",
				"options":     []string{"nosuid", "noexec", "nodev"},
			},
			{
				"destination": "/sys",
				"type":        "sysfs",
				"source":      "sysfs",
				"options":     []string{"nosuid", "noexec", "nodev", "ro"},
			},
			{
				"destination": "/sys/fs/cgroup",
				"type":        "cgroup",
				"source":      "cgroup",
				"options":     []string{"nosuid", "noexec", "nodev", "relatime", "ro"},
			},
		},
		"hostname": "vmrunner",
		"linux": map[string]any{
			"namespaces": []map[string]any{
				{"type": "pid"},
				{"type": "network"},
				{"type": "ipc"},
				{"type": "uts"},
				{"type": "mount"},
			},
		},
	}
	return json.MarshalIndent(spec, "", "  ")
}
