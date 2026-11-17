package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// imageCreate (docker pull) streams a minimal progress record. laboon pulls the
// image lazily on run and caches it, so this only satisfies the client.
func (d *daemon) imageCreate(w http.ResponseWriter, r *http.Request) {
	ref := r.URL.Query().Get("fromImage")
	if tag := r.URL.Query().Get("tag"); tag != "" && !strings.Contains(ref, ":") {
		ref += ":" + tag
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	enc.Encode(map[string]string{"status": "Pulling from " + ref})
	enc.Encode(map[string]string{"status": "Status: Image is available for " + ref})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

type createBody struct {
	Image        string
	Cmd          []string
	Entrypoint   []string
	Env          []string
	WorkingDir   string
	User         string
	Tty          bool
	AttachStdout bool
	AttachStderr bool
}

func (d *daemon) containerCreate(w http.ResponseWriter, r *http.Request) {
	var b createBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		apiError(w, http.StatusBadRequest, "bad create body: "+err.Error())
		return
	}
	if b.Image == "" {
		apiError(w, http.StatusBadRequest, "no image")
		return
	}
	c := newContainer(newID())
	c.name = r.URL.Query().Get("name")
	c.image, c.cmd, c.entry = b.Image, b.Cmd, b.Entrypoint
	c.env, c.workdir, c.user, c.tty = b.Env, b.WorkingDir, b.User, b.Tty
	d.mu.Lock()
	d.conts[c.id] = c
	d.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"Id": c.id, "Warnings": []string{}})
}

func (d *daemon) lookup(r *http.Request) *container {
	id := r.PathValue("id")
	d.mu.Lock()
	defer d.mu.Unlock()
	if c := d.conts[id]; c != nil {
		return c
	}
	for _, c := range d.conts { // accept a unique id prefix, as docker does
		if strings.HasPrefix(c.id, id) || c.name == id || c.name == "/"+id {
			return c
		}
	}
	return nil
}

func (d *daemon) containerStart(w http.ResponseWriter, r *http.Request) {
	c := d.lookup(r)
	if c == nil {
		apiError(w, http.StatusNotFound, "no such container")
		return
	}
	c.mu.Lock()
	if c.started {
		c.mu.Unlock()
		w.WriteHeader(http.StatusNotModified)
		return
	}
	c.started = true
	c.mu.Unlock()
	// Give a pending attach a moment to connect so early output is not lost.
	select {
	case <-c.attached:
	case <-time.After(2 * time.Second):
	}
	go c.run(d.allowNet)
	w.WriteHeader(http.StatusNoContent)
}

func (d *daemon) containerWait(w http.ResponseWriter, r *http.Request) {
	c := d.lookup(r)
	if c == nil {
		apiError(w, http.StatusNotFound, "no such container")
		return
	}
	// Send the response headers at once, then the status when the container
	// exits: the client calls wait before start, so it must not block here.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	<-c.done
	c.mu.Lock()
	code := c.exitCode
	c.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]any{"StatusCode": code})
}

func (d *daemon) containerAttach(w http.ResponseWriter, r *http.Request) {
	c := d.lookup(r)
	if c == nil {
		apiError(w, http.StatusNotFound, "no such container")
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		apiError(w, http.StatusInternalServerError, "attach not supported")
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		apiError(w, http.StatusInternalServerError, err.Error())
		return
	}
	ct := "application/vnd.docker.raw-stream"
	if !c.tty {
		ct = "application/vnd.docker.multiplexed-stream"
	}
	// The client asks to upgrade the connection to a raw stream; answer 101 with
	// the Upgrade headers it expects, else a plain 200 hijack.
	if strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade") {
		fmt.Fprintf(conn, "HTTP/1.1 101 UPGRADED\r\nContent-Type: %s\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n", ct)
	} else {
		fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: %s\r\n\r\n", ct)
	}
	c.attachMu.Lock()
	c.attachW = conn
	c.attachMu.Unlock()
	select {
	case <-c.attached:
	default:
		close(c.attached)
	}
	<-c.done // stream the container's output until it exits
	conn.Close()
}

func (d *daemon) containerDelete(w http.ResponseWriter, r *http.Request) {
	c := d.lookup(r)
	if c == nil {
		apiError(w, http.StatusNotFound, "no such container")
		return
	}
	if c.running() && r.URL.Query().Get("force") != "1" && r.URL.Query().Get("force") != "true" {
		apiError(w, http.StatusConflict, "container is running")
		return
	}
	d.mu.Lock()
	delete(d.conts, c.id)
	d.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (d *daemon) containerInspect(w http.ResponseWriter, r *http.Request) {
	c := d.lookup(r)
	if c == nil {
		apiError(w, http.StatusNotFound, "no such container")
		return
	}
	c.mu.Lock()
	code := c.exitCode
	c.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"Id":      c.id,
		"Name":    "/" + c.name,
		"Created": c.created.UTC().Format(time.RFC3339Nano),
		"Image":   c.image,
		"State": map[string]any{
			"Status":   c.status(),
			"Running":  c.running(),
			"ExitCode": code,
		},
		"Config": map[string]any{
			"Image":      c.image,
			"Cmd":        c.cmd,
			"Entrypoint": c.entry,
			"Env":        c.env,
			"Tty":        c.tty,
		},
	})
}

func (d *daemon) containerList(w http.ResponseWriter, r *http.Request) {
	all := r.URL.Query().Get("all") == "1" || r.URL.Query().Get("all") == "true"
	d.mu.Lock()
	var out []map[string]any
	for _, c := range d.conts {
		if !all && !c.running() {
			continue
		}
		name := c.name
		if name == "" {
			name = c.id[:12]
		}
		out = append(out, map[string]any{
			"Id":      c.id,
			"Names":   []string{"/" + name},
			"Image":   c.image,
			"Command": strings.Join(append(append([]string{}, c.entry...), c.cmd...), " "),
			"State":   c.status(),
			"Status":  c.status(),
			"Created": c.created.Unix(),
		})
	}
	d.mu.Unlock()
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, out)
}
