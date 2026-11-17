package build

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// isURL reports whether an ADD source is an http(s) URL rather than a context path.
func isURL(s string) bool {
	return strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://")
}

// fetchURL downloads url to the host path dest (a file), creating parents. ADD
// does not extract a URL even if it is an archive, matching docker. dest is
// inside the step's layer directory.
func fetchURL(url, dest string) error {
	cli := &http.Client{Timeout: 5 * time.Minute}
	resp, err := cli.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 16<<30)); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
