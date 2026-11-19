package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Spec is the subset of the OCI runtime configuration (config.json) qqbox maps
// to a run: the process, the root filesystem, and bind mounts.
type Spec struct {
	OCIVersion string   `json:"ociVersion"`
	Process    *Process `json:"process"`
	Root       *Root    `json:"root"`
	Hostname   string   `json:"hostname"`
	Mounts     []Mount  `json:"mounts"`
}

type Process struct {
	Terminal bool     `json:"terminal"`
	User     User     `json:"user"`
	Args     []string `json:"args"`
	Env      []string `json:"env"`
	Cwd      string   `json:"cwd"`
}

type User struct {
	UID uint32 `json:"uid"`
	GID uint32 `json:"gid"`
}

type Root struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly"`
}

type Mount struct {
	Destination string   `json:"destination"`
	Type        string   `json:"type"`
	Source      string   `json:"source"`
	Options     []string `json:"options"`
}

// loadSpec reads a bundle's config.json and resolves the absolute rootfs path.
func loadSpec(bundle string) (*Spec, string, error) {
	b, err := os.ReadFile(filepath.Join(bundle, "config.json"))
	if err != nil {
		return nil, "", err
	}
	var s Spec
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, "", fmt.Errorf("config.json: %w", err)
	}
	if s.Process == nil || len(s.Process.Args) == 0 {
		return nil, "", fmt.Errorf("config.json: no process args")
	}
	if s.Root == nil || s.Root.Path == "" {
		return nil, "", fmt.Errorf("config.json: no root path")
	}
	rootfs := s.Root.Path
	if !filepath.IsAbs(rootfs) {
		rootfs = filepath.Join(bundle, rootfs)
	}
	return &s, rootfs, nil
}
