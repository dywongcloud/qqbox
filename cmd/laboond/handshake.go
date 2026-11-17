package main

import (
	"net/http"
	"runtime"
	"time"
)

func (d *daemon) ping(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Builder-Version", "1")
	w.Header().Set("Docker-Experimental", "false")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", "0")
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		w.Write([]byte("OK"))
	}
}

func (d *daemon) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"Platform":      map[string]string{"Name": "laboon"},
		"Version":       laboonVersion,
		"ApiVersion":    apiVersion,
		"MinAPIVersion": minAPIVersion,
		"GitCommit":     "",
		"GoVersion":     runtime.Version(),
		"Os":            "linux",
		"Arch":          runtime.GOARCH,
		"KernelVersion": "laboon",
		"BuildTime":     time.Now().UTC().Format(time.RFC3339),
		"Components": []map[string]any{
			{"Name": "Engine", "Version": laboonVersion, "Details": map[string]string{
				"ApiVersion": apiVersion, "Os": "linux", "Arch": runtime.GOARCH,
			}},
		},
	})
}

func (d *daemon) info(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	n := len(d.conts)
	running := 0
	for _, c := range d.conts {
		if c.running() {
			running++
		}
	}
	d.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"ID":                "laboon",
		"Name":              "laboon",
		"ServerVersion":     laboonVersion,
		"Driver":            "laboon-vfs",
		"OperatingSystem":   "laboon (no second kernel)",
		"OSType":            "linux",
		"Architecture":      goArch(),
		"KernelVersion":     "laboon",
		"NCPU":              runtime.NumCPU(),
		"MemTotal":          0,
		"Containers":        n,
		"ContainersRunning": running,
		"ContainersPaused":  0,
		"ContainersStopped": n - running,
		"Images":            0,
		"DockerRootDir":     "/var/lib/laboon",
		"Runtimes":          map[string]any{"laboon": map[string]string{"path": "laboon"}},
		"DefaultRuntime":    "laboon",
	})
}
