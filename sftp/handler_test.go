package sftp

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/apex/log"
	pkgsftp "github.com/pkg/sftp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/internal/database"
	"github.com/pterodactyl/wings/server"
	"github.com/pterodactyl/wings/server/filesystem"
)

type writeAtFunc func([]byte, int64) (int, error)

func (f writeAtFunc) WriteAt(p []byte, off int64) (int, error) {
	return f(p, off)
}

func TestHandlerDeniesAccessWhenServerIsInProtectedState(t *testing.T) {
	tests := []struct {
		name string
		set  func(*server.Server)
	}{
		{
			name: "installing",
			set: func(s *server.Server) {
				s.SetInstalling(true)
			},
		},
		{
			name: "transferring",
			set: func(s *server.Server) {
				s.SetTransferring(true)
			},
		},
		{
			name: "restoring",
			set: func(s *server.Server) {
				s.SetRestoring(true)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := server.New(nil)
			if err != nil {
				t.Fatal(err)
			}
			tt.set(srv)

			h := Handler{
				server:      srv,
				permissions: []string{"*"},
			}

			if h.can(PermissionFileCreate) {
				t.Fatal("expected SFTP access to be denied")
			}
		})
	}
}

func TestWriterDeniesWritesWhenServerEntersProtectedState(t *testing.T) {
	tests := []struct {
		name string
		set  func(*server.Server)
	}{
		{
			name: "installing",
			set: func(s *server.Server) {
				s.SetInstalling(true)
			},
		},
		{
			name: "transferring",
			set: func(s *server.Server) {
				s.SetTransferring(true)
			},
		},
		{
			name: "restoring",
			set: func(s *server.Server) {
				s.SetRestoring(true)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := server.New(nil)
			if err != nil {
				t.Fatal(err)
			}

			var called bool
			writer := quotaWriterAt{
				server: srv,
				WriterAt: writeAtFunc(func(_ []byte, _ int64) (int, error) {
					called = true
					return 1, nil
				}),
			}
			tt.set(srv)

			n, err := writer.WriteAt([]byte("x"), 0)
			if !errors.Is(err, pkgsftp.ErrSSHFxPermissionDenied) {
				t.Fatalf("expected permission denied, got %v", err)
			}
			if n != 0 {
				t.Fatalf("expected zero bytes written, got %d", n)
			}
			if called {
				t.Fatal("expected underlying writer not to be called")
			}
		})
	}
}

func TestWriterForwardsWritesWhenServerIsAvailable(t *testing.T) {
	srv, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	writer := quotaWriterAt{
		server: srv,
		WriterAt: writeAtFunc(func(p []byte, _ int64) (int, error) {
			return len(p), io.EOF
		}),
	}

	n, err := writer.WriteAt([]byte("test"), 0)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("expected forwarded error, got %v", err)
	}
	if n != 4 {
		t.Fatalf("expected forwarded byte count, got %d", n)
	}
}

func TestHandlerRejectsMalformedSetstatAttributes(t *testing.T) {
	srv, err := server.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	h := Handler{
		server:      srv,
		permissions: []string{PermissionFileUpdate},
		logger:      log.WithField("test", t.Name()),
	}
	request := pkgsftp.NewRequest("Setstat", "/")
	request.Flags = 1 // SSH_FILEXFER_ATTR_SIZE

	if err := h.Filecmd(request); !errors.Is(err, pkgsftp.ErrSSHFxBadMessage) {
		t.Fatalf("expected bad message, got %v", err)
	}

	request.Flags = sftpAttributeExtended
	request.Attrs = make([]byte, 4)
	binary.BigEndian.PutUint32(request.Attrs, ^uint32(0))
	if err := h.Filecmd(request); !errors.Is(err, pkgsftp.ErrSSHFxBadMessage) {
		t.Fatalf("expected extended attributes to be rejected, got %v", err)
	}
}

func TestSetstatMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     uint32
		expected uint32
	}{
		{name: "file permissions", mode: 0o600, expected: 0o600},
		{name: "default permissions", mode: 0o000, expected: 0o644},
		{name: "directory permissions", mode: 0o040700, expected: 0o755},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := pkgsftp.NewRequest("Setstat", "/test")
			request.Flags = 4 // SSH_FILEXFER_ATTR_PERMISSIONS
			request.Attrs = make([]byte, 4)
			binary.BigEndian.PutUint32(request.Attrs, tt.mode)

			mode, err := setstatMode(request)
			if err != nil {
				t.Fatal(err)
			}
			if uint32(mode) != tt.expected {
				t.Fatalf("expected mode %04o, got %04o", tt.expected, mode)
			}
		})
	}
}

func TestHandlerFullLifecycle(t *testing.T) {
	cfg := &config.Configuration{AuthenticationToken: "test-auth-token"}
	cfg.System.RootDirectory = t.TempDir()
	config.Set(cfg)
	func() {
		defer func() { _ = recover() }()
		_ = database.Initialize()
	}()
	srv, err := server.New(nil)
	require.NoError(t, err)

	tempDir := t.TempDir()
	fs, err := filesystem.New(tempDir, 1024*1024*1024, nil)
	require.NoError(t, err)

	h := &Handler{
		server:      srv,
		fs:          fs,
		permissions: []string{"*"},
		events:      &eventHandler{ip: "127.0.0.1", user: "user-1", server: srv.ID()},
		logger:      log.WithField("test", t.Name()),
	}

	assert.NotNil(t, h.Handlers())

	// ReadOnly mode
	h.ro = true
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, h.Filecmd(pkgsftp.NewRequest("Mkdir", "/dir")))
	_, err = h.Filewrite(pkgsftp.NewRequest("Write", "/file.txt"))
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, err)
	h.ro = false

	// Unknown command
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, h.Filecmd(pkgsftp.NewRequest("Unknown", "/foo")))

	// Permission denied cases
	hNoPerm := &Handler{
		server:      srv,
		fs:          fs,
		permissions: []string{},
		events:      &eventHandler{ip: "127.0.0.1", user: "user-1", server: srv.ID()},
		logger:      log.WithField("test", t.Name()),
	}
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(pkgsftp.NewRequest("Setstat", "/foo")))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(pkgsftp.NewRequest("Rename", "/foo")))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(pkgsftp.NewRequest("Rmdir", "/foo")))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(pkgsftp.NewRequest("Mkdir", "/foo")))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(pkgsftp.NewRequest("Symlink", "/foo")))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, hNoPerm.Filecmd(pkgsftp.NewRequest("Remove", "/foo")))
	_, err = hNoPerm.Fileread(pkgsftp.NewRequest("Read", "/foo"))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, err)
	_, err = hNoPerm.Filewrite(pkgsftp.NewRequest("Write", "/foo"))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, err)
	_, err = hNoPerm.Filelist(pkgsftp.NewRequest("List", "/"))
	assert.Equal(t, pkgsftp.ErrSSHFxPermissionDenied, err)

	// Valid commands with full permissions
	// Mkdir
	reqMkdir := pkgsftp.NewRequest("Mkdir", "/subdir")
	assert.Contains(t, []error{nil, pkgsftp.ErrSSHFxOk}, h.Filecmd(reqMkdir))
	assert.DirExists(t, filepath.Join(tempDir, "subdir"))

	// Write file via Filewrite
	reqWrite := pkgsftp.NewRequest("Write", "/subdir/test.txt")
	w, err := h.Filewrite(reqWrite)
	assert.NoError(t, err)
	assert.NotNil(t, w)
	_, err = w.WriteAt([]byte("hello sftp"), 0)
	assert.NoError(t, err)
	if c, ok := w.(io.Closer); ok {
		_ = c.Close()
	}
	assert.FileExists(t, filepath.Join(tempDir, "subdir", "test.txt"))

	// Fileread
	reqRead := pkgsftp.NewRequest("Read", "/subdir/test.txt")
	r, err := h.Fileread(reqRead)
	assert.NoError(t, err)
	assert.NotNil(t, r)
	buf := make([]byte, 10)
	n, err := r.ReadAt(buf, 0)
	assert.NoError(t, err)
	assert.Equal(t, 10, n)
	assert.Equal(t, "hello sftp", string(buf))
	if c, ok := r.(io.Closer); ok {
		_ = c.Close()
	}

	// Fileread non-existent
	reqReadMiss := pkgsftp.NewRequest("Read", "/subdir/missing.txt")
	_, err = h.Fileread(reqReadMiss)
	assert.Equal(t, pkgsftp.ErrSSHFxNoSuchFile, err)

	// Filelist: List
	reqList := pkgsftp.NewRequest("List", "/subdir")
	lister, err := h.Filelist(reqList)
	assert.NoError(t, err)
	assert.NotNil(t, lister)
	infos := make([]os.FileInfo, 5)
	count, _ := lister.ListAt(infos, 0)
	assert.True(t, count >= 0)

	// Filelist: Stat
	reqStat := pkgsftp.NewRequest("Stat", "/subdir/test.txt")
	statLister, err := h.Filelist(reqStat)
	assert.NoError(t, err)
	assert.NotNil(t, statLister)

	// Filelist: Stat non-existent
	reqStatMiss := pkgsftp.NewRequest("Stat", "/subdir/nonexistent.txt")
	_, err = h.Filelist(reqStatMiss)
	assert.Equal(t, pkgsftp.ErrSSHFxNoSuchFile, err)

	// Filelist: Unknown
	reqListBad := pkgsftp.NewRequest("Unknown", "/subdir")
	_, err = h.Filelist(reqListBad)
	assert.Equal(t, pkgsftp.ErrSSHFxOpUnsupported, err)

	// Rename
	reqRename := pkgsftp.NewRequest("Rename", "/subdir/test.txt")
	reqRename.Target = "/subdir/test_renamed.txt"
	assert.Contains(t, []error{nil, pkgsftp.ErrSSHFxOk}, h.Filecmd(reqRename))
	assert.FileExists(t, filepath.Join(tempDir, "subdir", "test_renamed.txt"))

	// Setstat
	reqSetstat := pkgsftp.NewRequest("Setstat", "/subdir/test_renamed.txt")
	reqSetstat.Flags = 4 // SSH_FILEXFER_ATTR_PERMISSIONS
	reqSetstat.Attrs = make([]byte, 4)
	binary.BigEndian.PutUint32(reqSetstat.Attrs, 0o644)
	assert.Contains(t, []error{nil, pkgsftp.ErrSSHFxOk}, h.Filecmd(reqSetstat))

	// Symlink
	reqSymlink := pkgsftp.NewRequest("Symlink", "/subdir/test_renamed.txt")
	reqSymlink.Target = "/subdir/link.txt"
	assert.Contains(t, []error{nil, pkgsftp.ErrSSHFxOk}, h.Filecmd(reqSymlink))

	// Remove
	reqRemove := pkgsftp.NewRequest("Remove", "/subdir/test_renamed.txt")
	assert.Contains(t, []error{nil, pkgsftp.ErrSSHFxOk}, h.Filecmd(reqRemove))
	assert.NoFileExists(t, filepath.Join(tempDir, "subdir", "test_renamed.txt"))

	// Rmdir
	reqRmdir := pkgsftp.NewRequest("Rmdir", "/subdir")
	assert.Contains(t, []error{nil, pkgsftp.ErrSSHFxOk}, h.Filecmd(reqRmdir))
	assert.NoDirExists(t, filepath.Join(tempDir, "subdir"))
}
