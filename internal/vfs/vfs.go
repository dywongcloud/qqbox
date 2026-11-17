// Package vfs is laboon's layered, copy-on-write filesystem.
//
// It is an overlay over real host directories, not an in-memory store: the
// guest execs and mmaps its files, so file data stays in real host files that
// the supervisor hands to the guest. The vfs owns the layering, the copy-up on
// write, and the metadata a plain host directory cannot hold — which paths a
// layer deletes (whiteouts), which directories hide everything below them
// (opaque), and the guest-visible mode and owner of each path, which differ
// from the host file's (the guest runs as root and the host files belong to
// the unprivileged user running laboon).
//
// The guest runs on an overlayFS: a union of layer FileSystems with the writable
// one on top, copy-up and whiteouts going through the FileSystem interface and a
// shared MetaStore. Because a Layer's metadata is explicit rather than kept in
// memory, a layer serializes straight to and from an OCI image layer.
package vfs
