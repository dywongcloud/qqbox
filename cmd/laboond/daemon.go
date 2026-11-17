package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"runtime"
	"sync"
)

// apiVersion is the Docker Engine API version laboond reports; a client
// negotiates down to its own if older.
const (
	apiVersion    = "1.43"
	minAPIVersion = "1.24"
	laboonVersion = "0.1.0"
)

// daemon holds the running containers and whether the network is allowed.
type daemon struct {
	allowNet bool

	mu    sync.Mutex
	conts map[string]*container
	seq   int
}

func newDaemon(allowNet bool) *daemon {
	return &daemon{allowNet: allowNet, conts: map[string]*container{}}
}

// versionPrefix matches the optional /v1.43 that clients prepend; the handlers
// are registered without it.
var versionPrefix = regexp.MustCompile(`^/v[0-9]+\.[0-9]+`)

func (d *daemon) router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /_ping", d.ping)
	mux.HandleFunc("HEAD /_ping", d.ping)
	mux.HandleFunc("GET /version", d.version)
	mux.HandleFunc("GET /info", d.info)

	mux.HandleFunc("POST /images/create", d.imageCreate)

	mux.HandleFunc("POST /containers/create", d.containerCreate)
	mux.HandleFunc("GET /containers/json", d.containerList)
	mux.HandleFunc("GET /containers/{id}/json", d.containerInspect)
	mux.HandleFunc("POST /containers/{id}/start", d.containerStart)
	mux.HandleFunc("POST /containers/{id}/wait", d.containerWait)
	mux.HandleFunc("POST /containers/{id}/attach", d.containerAttach)
	mux.HandleFunc("DELETE /containers/{id}", d.containerDelete)

	// Strip the /v1.43 version prefix a client prepends, and always announce the
	// API version so the handshake negotiates.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Api-Version", apiVersion)
		w.Header().Set("Ostype", "linux")
		if m := versionPrefix.FindString(r.URL.Path); m != "" {
			r.URL.Path = r.URL.Path[len(m):]
			if r.URL.Path == "" {
				r.URL.Path = "/"
			}
		}
		mux.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

// apiError is the Docker API error envelope: {"message": "..."}.
func apiError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"message": msg})
}

func goArch() string {
	if runtime.GOARCH == "amd64" {
		return "x86_64"
	}
	return runtime.GOARCH
}
