package filesystem

import (
	"flag"
	"fmt"
	"io"
	fsys "io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
	"github.com/gabriel-vasile/mimetype"
	ignore "github.com/sabhiram/go-gitignore"

	"github.com/Aquadactyl/manta/config"
)

type Filesystem struct {
	root      *os.Root
	rootPath  string
	diskLimit atomic.Int64
	diskUsed  atomic.Int64

	// mu guards changes to the disk usage that must be checked against the
	// limit, along with the fields tracking reservations below.
	mu sync.RWMutex
	// reserved is the space reserved by writes that have not finished yet.
	reserved int64
	// walking is set while the disk usage is being calculated, during which
	// walkReserved is the space reserved by writes in progress at any point.
	walking      bool
	walkReserved int64

	// lookupMu prevents the disk usage from being calculated more than once
	// at the same time.
	lookupMu          sync.Mutex
	lastLookupTime    *usageLookupTime
	lookupInProgress  atomic.Bool
	diskCheckInterval time.Duration
	denylist          *ignore.GitIgnore

	isTest bool
}

type filesystemFile interface {
	io.Reader
	io.ReaderAt
	io.Seeker
	io.Closer
	Stat() (os.FileInfo, error)
}

var removeFilesystemContents = func(fs *Filesystem) error {
	entries, err := fs.root.Open(".")
	if err != nil {
		return err
	}
	defer entries.Close()
	dirs, err := entries.ReadDir(-1)
	if err != nil {
		return err
	}
	for _, entry := range dirs {
		if err := fs.root.RemoveAll(entry.Name()); err != nil {
			return err
		}
	}
	return nil
}

var (
	filesystemOpenRoot = os.OpenRoot
	filesystemIsRoot   = func() bool { return os.Geteuid() == 0 }
	filesystemLchown   = func(root *os.Root, p string, uid, gid int) error {
		return root.Lchown(normalize(p), uid, gid)
	}
	filesystemTouch = func(fs *Filesystem, p string, flag int, mode os.FileMode) (*os.File, error) {
		return fs.root.OpenFile(normalize(p), flag, mode)
	}
	filesystemOpen = func(fs *Filesystem, p string) (filesystemFile, error) {
		return fs.root.Open(normalize(p))
	}
)

// New creates a new Filesystem instance for a given server.
func New(root string, size int64, denylist []string) (*Filesystem, error) {
	if err := os.MkdirAll(root, 0o755); err != nil { //nolint:gosec // mounted as the container's home directory; the parent data directory is 0700
		return nil, err
	}
	openedRoot, err := filesystemOpenRoot(root)
	if err != nil {
		return nil, err
	}
	fs := &Filesystem{
		root:     openedRoot,
		rootPath: root,

		diskCheckInterval: time.Duration(config.Get().System.DiskCheckInterval),
		lastLookupTime:    &usageLookupTime{},
		denylist:          ignore.CompileIgnoreLines(denylist...),
		isTest:            flag.Lookup("test.v") != nil,
	}
	fs.diskLimit.Store(size)
	return fs, nil
}

// SetTest sets the test mode flag for this Filesystem instance.
func (fs *Filesystem) SetTest(b bool) {
	fs.isTest = b
}

// Path returns the root path for the Filesystem instance.
func (fs *Filesystem) Path() string {
	return fs.rootPath
}

// Close releases the root directory handle.
func (fs *Filesystem) Close() error { return fs.root.Close() }

func normalize(p string) string {
	// Treat an absolute-looking user path as relative to the root, but strip
	// its separator before cleaning so leading `..` components remain visible
	// to os.Root and are rejected instead of being clamped to the root.
	clean := filepath.Clean(strings.TrimLeft(p, string(filepath.Separator)))
	if clean == "" {
		return "."
	}
	return clean
}

func (fs *Filesystem) Open(p string) (*os.File, error) {
	return fs.root.Open(normalize(p))
}

func (fs *Filesystem) OpenFile(p string, flag int, mode os.FileMode) (*os.File, error) {
	return fs.root.OpenFile(normalize(p), flag, mode)
}

func (fs *Filesystem) Lstat(p string) (os.FileInfo, error) {
	return fs.root.Lstat(normalize(p))
}

func (fs *Filesystem) Readlink(p string) (string, error) {
	return fs.root.Readlink(normalize(p))
}

func (fs *Filesystem) Remove(p string) error { return fs.root.Remove(normalize(p)) }

func (fs *Filesystem) RemoveAll(p string) error {
	return fs.root.RemoveAll(normalize(p))
}

func (fs *Filesystem) MkdirAll(p string, mode os.FileMode) error {
	return fs.root.MkdirAll(normalize(p), mode)
}

func (fs *Filesystem) ReadDir(p string) ([]os.DirEntry, error) {
	f, err := fs.root.Open(normalize(p))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.ReadDir(-1)
}

// ReadDir reads directory entries.
// ReadDirStat is like ReadDir except that it returns FileInfo for each entry
// instead of just a DirEntry.
func (fs *Filesystem) ReadDirStat(path string) ([]os.FileInfo, error) {
	entries, err := fs.ReadDir(path)
	if err != nil {
		return nil, err
	}
	infos := make([]os.FileInfo, 0, len(entries))
	for _, entry := range entries {
		info, err := fs.root.Lstat(filepath.Join(normalize(path), entry.Name()))
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// File returns a reader for a file instance as well as the stat information.
func (fs *Filesystem) File(p string) (interface {
	io.ReadCloser
	io.ReaderAt
}, Stat, error) {
	f, err := filesystemOpen(fs, p)
	if err != nil {
		return nil, Stat{}, err
	}
	st, err := statFromFile(f)
	if err != nil {
		_ = f.Close()
		return nil, Stat{}, err
	}
	return f, st, nil
}

// Touch acts by creating the given file and path on the disk if it is not present
// already. If  it is present, the file is opened using the defaults which will truncate
// the contents. The opened file is then returned to the caller.
func (fs *Filesystem) Touch(p string, flag int) (*quotaFile, error) {
	file, currentSize, err := fs.openForWrite(p, flag, 0o644)
	if err != nil {
		return nil, err
	}
	return newQuotaFile(fs, file, currentSize), nil
}

// openForWrite opens p with the given flags, creating it if it does not exist,
// and returns it along with the size it had when it was opened. The size is read
// from the opened file rather than from the path, since the path can be changed,
// for example to a symlink, between being checked and being opened. If flag
// includes O_TRUNC the file is truncated after its size is read.
func (fs *Filesystem) openForWrite(p string, flag int, mode os.FileMode) (*os.File, int64, error) {
	if err := fs.mkdirAll(filepath.Dir(normalize(p)), 0o755); err != nil {
		return nil, 0, err
	}
	file, err := filesystemTouch(fs, p, flag&^os.O_TRUNC|os.O_CREATE, mode)
	if err != nil {
		return nil, 0, err
	}
	st, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, 0, err
	}
	var size int64
	if st.Mode().IsRegular() {
		size = st.Size()
	}
	if flag&os.O_TRUNC != 0 {
		if err := file.Truncate(0); err != nil {
			_ = file.Close()
			return nil, 0, err
		}
	}
	return file, size, nil
}

// checkNotDirectory returns an error if p is a directory.
func (fs *Filesystem) checkNotDirectory(p string) error {
	if st, err := fs.Lstat(p); err == nil && st.IsDir() {
		return errors.WithStack(&Error{code: ErrCodeIsDirectory, path: p, resolved: p})
	}
	return nil
}

// Writefile writes a file to the system. If the file does not already exist one
// will be created. This will also properly recalculate the disk space used by
// the server when writing new files or modifying existing ones.
//
// DEPRECATED: use `Write` instead.
func (fs *Filesystem) Writefile(p string, r io.Reader) error {
	if err := fs.checkNotDirectory(p); err != nil {
		return err
	}

	// Touch the file and return the handle to it at this point. This will
	// create or truncate the file, and create any necessary parent directories
	// if they are missing.
	file, currentSize, err := fs.openForWrite(p, os.O_RDWR|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("error touching file: %w", err)
	}
	defer file.Close()

	// Do not use CopyBuffer here, it is wasteful as the file implements
	// io.ReaderFrom, which causes it to not use the buffer anyways.
	n, err := io.Copy(file, r)

	// Adjust the disk usage to account for the old size and the new size of the file.
	fs.addDisk(n - currentSize)

	if err := fs.chownFile(p); err != nil {
		return fmt.Errorf("error chowning file: %w", err)
	}
	// Return the error from io.Copy.
	return err
}

func (fs *Filesystem) Write(p string, r io.Reader, newSize int64, mode os.FileMode) error {
	if err := fs.checkNotDirectory(p); err != nil {
		return err
	}

	// Ensure the parent directories exist and are owned by the server user
	// before creating the file. Touch would create any missing parents
	// implicitly, but as the user Wings runs as; creating them here lets us
	// chown the ones we add.
	if err := fs.mkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}

	_, statErr := fs.Lstat(p)
	existed := statErr == nil

	// Open the file before reserving space, so that the size being replaced is the
	// size of the file that is written to.
	file, currentSize, err := fs.openForWrite(p, os.O_RDWR, mode)
	if err != nil {
		return err
	}
	defer file.Close()

	// Check that the new size we're writing to the disk can fit, and reserve it so that
	// writes happening at the same time cannot each use the space that is left. If there
	// is currently a file we'll subtract that current file size from the size of the buffer
	// to determine the amount of new data we're writing (or amount we're removing if smaller).
	if err := fs.reserveDisk(newSize - currentSize); err != nil {
		if !existed {
			_ = fs.Remove(p)
		}
		return err
	}
	reserved := max(newSize-currentSize, 0)
	// Release the reservation even if reading r panics, since the panic is
	// recovered and the reservation would otherwise last until Wings restarts.
	var used int64
	defer func() { fs.releaseDisk(reserved, used) }()

	if err := file.Truncate(0); err != nil {
		return err
	}
	// The file is now empty.
	used = -currentSize

	var n int64
	if newSize > 0 {
		// Do not use CopyBuffer here, it is wasteful as the file implements
		// io.ReaderFrom, which causes it to not use the buffer anyways.
		n, err = io.Copy(file, io.LimitReader(r, newSize))
	}

	// Adjust the disk usage to account for the old size and the new size of the file.
	used = n - currentSize

	if err := fs.chownFile(p); err != nil {
		return err
	}
	// Return any remaining error.
	return err
}

// WriteFrom writes the contents of r to p, creating the file or truncating it
// if it exists, and returns the number of bytes written. Unlike Write, the size
// of the contents does not need to be known ahead of time: each write is counted
// against the disk limit as it happens. If writing fails the file is removed.
func (fs *Filesystem) WriteFrom(p string, r io.Reader) (int64, error) {
	if err := fs.checkNotDirectory(p); err != nil {
		return 0, err
	}

	if err := fs.mkdirAll(filepath.Dir(p), 0o755); err != nil {
		return 0, err
	}

	file, err := fs.Touch(p, os.O_RDWR|os.O_TRUNC)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(file, r)
	if cerr := file.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = fs.chownFile(p)
	}
	if err != nil {
		_ = fs.Remove(p)
		return n, err
	}
	return n, nil
}

// CreateDirectory creates a new directory (name) at a specified path (p) for
// the server.
func (fs *Filesystem) CreateDirectory(name string, p string) error {
	return fs.mkdirAll(filepath.Join(p, name), 0o755)
}

func (fs *Filesystem) Rename(oldpath, newpath string) error {
	oldpath, newpath = normalize(oldpath), normalize(newpath)
	if _, err := fs.root.Lstat(newpath); err == nil {
		return os.ErrExist
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := fs.root.MkdirAll(filepath.Dir(newpath), 0o755); err != nil {
		return err
	}
	return fs.root.Rename(oldpath, newpath)
}

func (fs *Filesystem) Symlink(oldpath, newpath string) error {
	return fs.root.Symlink(oldpath, normalize(newpath))
}

func (fs *Filesystem) chownFile(name string) error {
	if fs.isTest || !filesystemIsRoot() {
		return nil
	}

	uid := config.Get().System.User.UID
	gid := config.Get().System.User.GID
	return filesystemLchown(fs.root, name, uid, gid)
}

// mkdirAll creates the directory p along with any missing parents, chowning
// every directory it creates to the server user so they are not left owned by
// the user Wings runs as.
func (fs *Filesystem) mkdirAll(p string, mode os.FileMode) error {
	p = normalize(p)
	created, err := fs.missingDirectories(p)
	if err != nil {
		return err
	}
	if err := fs.root.MkdirAll(p, mode); err != nil {
		return err
	}
	for _, dir := range created {
		if err := fs.chownFile(dir); err != nil {
			return err
		}
	}
	return nil
}

func (fs *Filesystem) missingDirectories(p string) ([]string, error) {
	var missing []string
	for current := p; current != "." && current != string(filepath.Separator); current = filepath.Dir(current) {
		info, err := fs.root.Stat(current)
		if err == nil {
			if !info.IsDir() {
				return nil, syscall.ENOTDIR
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, current)
	}
	slices.Reverse(missing)
	return missing, nil
}

// Chown recursively iterates over a file or directory and sets the permissions on all of the
// underlying files. Iterate over all of the files and directories. If it is a file just
// go ahead and perform the chown operation. Otherwise dig deeper into the directory until
// we've run out of directories to dig into.
func (fs *Filesystem) Chown(p string) error {
	if fs.isTest || !filesystemIsRoot() {
		return nil
	}

	uid := config.Get().System.User.UID
	gid := config.Get().System.User.GID

	p = normalize(p)
	if err := filesystemLchown(fs.root, p, uid, gid); err != nil {
		return errors.Wrap(err, "server/filesystem: chown: failed to chown path")
	}

	// If this is not a directory we can now return from the function, there is nothing
	// left that we need to do.
	if st, err := fs.root.Lstat(p); err != nil || !st.IsDir() {
		return nil
	}
	if err := fs.WalkDir(p, func(name string, info os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if name == p {
			return nil
		}
		if err := filesystemLchown(fs.root, name, uid, gid); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return fmt.Errorf("server/filesystem: chown: failed to chown during walk function: %w", err)
	}
	return nil
}

func (fs *Filesystem) Chmod(path string, mode os.FileMode) error {
	return fs.root.Chmod(normalize(path), mode)
}

// Begin looping up to 50 times to try and create a unique copy file name. This will take
// an input of "file.txt" and generate "file copy.txt". If that name is already taken, it will
// then try to write "file copy 2.txt" and so on, until reaching 50 loops. At that point we
// won't waste anymore time, just use the current timestamp and make that copy.
//
// Could probably make this more efficient by checking if there are any files matching the copy
// pattern, and trying to find the highest number and then incrementing it by one rather than
// looping endlessly.
func (fs *Filesystem) findCopySuffix(dir, name, extension string) (string, error) {
	var i int
	suffix := " copy"

	for i = 0; i < 51; i++ {
		if i > 0 {
			suffix = " copy " + strconv.Itoa(i)
		}

		n := name + suffix + extension
		// If we stat the file and it does not exist that means we're good to create the copy. If it
		// does exist, we'll just continue to the next loop and try again.
		if _, err := fs.root.Lstat(filepath.Join(dir, n)); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				return "", err
			}
			break
		}

		if i == 50 {
			suffix = "copy." + time.Now().Format(time.RFC3339)
		}
	}

	return name + suffix + extension, nil
}

// Copy copies a given file to the same location and appends a suffix to the
// file to indicate that it has been copied.
func (fs *Filesystem) Copy(p string) error {
	p = normalize(p)
	source, err := filesystemOpen(fs, p)
	if err != nil {
		return err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return err
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		// If this is a directory or not a regular file, just throw a not-exist error
		// since anything calling this function should understand what that means.
		return os.ErrNotExist
	}
	currentSize := info.Size()

	// Check that copying this file wouldn't put the server over its limit, and reserve
	// the space so that copies happening at the same time cannot each use it.
	if err := fs.reserveDisk(currentSize); err != nil {
		return err
	}
	// Release the reservation even if the copy panics, see Write.
	var used int64
	defer func() { fs.releaseDisk(currentSize, used) }()

	dir := filepath.Dir(p)
	base := filepath.Base(p)
	extension := filepath.Ext(base)
	baseName := strings.TrimSuffix(base, extension)

	// Ensure that ".tar" is also counted as apart of the file extension.
	// There might be a better way to handle this for other double file extensions,
	// but this is a good workaround for now.
	if strings.HasSuffix(baseName, ".tar") {
		extension = ".tar" + extension
		baseName = strings.TrimSuffix(baseName, ".tar")
	}

	// Never write into an existing file, such as a copy made by another request
	// at the same time.
	var (
		newName string
		dst     *os.File
	)
	for i := 0; ; i++ {
		newName, err = fs.findCopySuffix(dir, baseName, extension)
		if err == nil {
			dst, err = fs.root.OpenFile(filepath.Join(dir, newName), os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode())
		}
		if err == nil {
			break
		}
		if !errors.Is(err, os.ErrExist) || i >= 10 {
			return err
		}
	}
	defer dst.Close()

	// Do not use CopyBuffer here, it is wasteful as the file implements
	// io.ReaderFrom, which causes it to not use the buffer anyways.
	n, err := io.Copy(dst, io.LimitReader(source, currentSize))
	used = n

	if !fs.isTest && filesystemIsRoot() {
		if err := filesystemLchown(fs.root, filepath.Join(dir, newName), config.Get().System.User.UID, config.Get().System.User.GID); err != nil {
			return err
		}
	}
	// Return the error from io.Copy.
	return err
}

// TruncateRootDirectory removes _all_ files and directories from a server's
// data directory and resets the used disk space to zero. The directory itself
// is kept, since it is mounted into the server's container.
func (fs *Filesystem) TruncateRootDirectory() error {
	if err := removeFilesystemContents(fs); err != nil {
		return err
	}
	fs.mu.Lock()
	defer fs.mu.Unlock()
	// Space reserved by writes that are still in progress remains in use.
	fs.diskUsed.Store(fs.reserved)
	if fs.isTest {
		fs.diskLimit.Store(0)
	}
	return nil
}

// Delete removes a file or folder from the system. Prevents the user from
// accidentally (or maliciously) removing their root server data directory.
func (fs *Filesystem) Delete(p string) error {
	p = normalize(p)
	hasHardLinks := fs.containsHardLinks(p)
	size, _ := fs.DirectorySize(p)
	if err := fs.root.RemoveAll(p); err != nil {
		return err
	}
	if hasHardLinks {
		_, err := fs.updateCachedDiskUsage()
		return err
	}
	fs.addDisk(-size)
	return nil
}

//	type fileOpener struct {
//		fs   *Filesystem
//		busy uint
//	}
//
// // Attempts to open a given file up to "attempts" number of times, using a backoff. If the file
// // cannot be opened because of a "text file busy" error, we will attempt until the number of attempts
// // has been exhaused, at which point we will abort with an error.
// ListDirectory lists the contents of a given directory and returns stat
// information about each file and folder within it.
func (fs *Filesystem) ListDirectory(p string) ([]Stat, error) {
	entries, err := fs.ReadDir(p)
	if err != nil {
		return nil, err
	}
	out := make([]Stat, 0, len(entries))
	for _, entry := range entries {
		st, err := statDirectoryEntry(rootedDirEntry{fs: fs, parent: normalize(p), entry: entry})
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}

	// Put folders first and sort each group alphabetically in one pass.
	slices.SortFunc(out, func(a, b Stat) int {
		if a.IsDir() != b.IsDir() {
			if a.IsDir() {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Name(), b.Name())
	})

	return out, nil
}

// SearchFiles returns paths to regular files beneath directory whose path
// contains query, case-insensitively. Paths are relative to the server root.
// The walk is confined to the server filesystem and stops once limit matches
// are found.
func (fs *Filesystem) SearchFiles(directory, query string, limit int) ([]string, error) {
	query = strings.ToLower(query)
	if query == "" || limit <= 0 {
		return nil, nil
	}

	root := normalize(directory)
	paths := make([]string, 0, limit)
	err := fsys.WalkDir(fs.root.FS(), root, func(walked string, entry fsys.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry == nil || !entry.Type().IsRegular() || walked == root {
			return nil
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(walked, root), "/")
		match := relative
		if root != "." {
			match = filepath.ToSlash(filepath.Join(root, relative))
		}
		if strings.Contains(strings.ToLower(match), query) {
			paths = append(paths, match)
			if len(paths) >= limit {
				return fsys.SkipAll
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return paths, nil
}

type rootedDirEntry struct {
	fs     *Filesystem
	parent string
	entry  os.DirEntry
}

func (e rootedDirEntry) Name() string      { return e.entry.Name() }
func (e rootedDirEntry) IsDir() bool       { return e.entry.IsDir() }
func (e rootedDirEntry) Type() os.FileMode { return e.entry.Type() }
func (e rootedDirEntry) Info() (os.FileInfo, error) {
	return e.fs.root.Lstat(filepath.Join(e.parent, e.entry.Name()))
}
func (e rootedDirEntry) Open() (*os.File, error) {
	return e.fs.root.Open(filepath.Join(e.parent, e.entry.Name()))
}

func (fs *Filesystem) WalkDir(root string, fn func(string, fsys.DirEntry, error) error) error {
	return fsys.WalkDir(fs.root.FS(), normalize(root), fn)
}

func statDirectoryEntry(e rootedDirEntry) (Stat, error) {
	info, err := e.Info()
	if err != nil {
		return Stat{}, err
	}

	var d string
	if e.Type().IsDir() {
		d = "inode/directory"
	} else {
		d = "application/octet-stream"
	}
	var m *mimetype.MIME
	if e.Type().IsRegular() {
		f, err := e.Open()
		if err == nil {
			m, err = detectFileMIME(f)
			_ = f.Close()
		}
		if err != nil {
			log.Error(err.Error())
		}
	}

	st := Stat{FileInfo: info, Mimetype: d}
	if m != nil {
		st.Mimetype = m.String()
	}
	return st, nil
}

func (fs *Filesystem) Chtimes(path string, atime, mtime time.Time) error {
	if fs.isTest {
		return nil
	}
	return fs.root.Chtimes(normalize(path), atime, mtime)
}
