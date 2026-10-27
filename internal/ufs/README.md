# Filesystem

The `ufs` package provides a secure, sandboxed Unix filesystem implementation. It enforces
directory boundaries using Linux `openat2` resolution flags (`RESOLVE_BENEATH` and `RESOLVE_NO_SYMLINKS`),
preventing symlink attacks and directory traversal while providing per-server disk quota enforcement.

## Licensing

Most code in this package is licensed under `MIT` with some exceptions.

The following files are licensed under `BSD-3-Clause` due to them being copied
verbatim or derived from [Go](https://go.dev)'s source code.

- [`file_posix.go`](./file_posix.go)
- [`mkdir_unix.go`](./mkdir_unix.go)
- [`path_unix.go`](./path_unix.go)
- [`removeall_unix.go`](./removeall_unix.go)
- [`stat_unix.go`](./stat_unix.go)
- [`walk.go`](./walk.go)

These changes are not associated with nor endorsed by The Go Authors.
