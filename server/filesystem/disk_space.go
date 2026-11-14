package filesystem

import (
	fsys "io/fs"
	"os"
	"slices"
	"sync/atomic"
	"syscall"
	"time"

	"emperror.dev/errors"
	"github.com/apex/log"
)

type SpaceCheckingOpts struct {
	AllowStaleResponse bool
}

type usageLookupTime struct {
	value atomic.Pointer[time.Time]
}

var (
	walkDirectoryForSize = func(root fsys.FS, name string, fn fsys.WalkDirFunc) error {
		return fsys.WalkDir(root, name, fn)
	}
	lstatForDirectorySize = func(root *os.Root, name string) (os.FileInfo, error) {
		return root.Lstat(name)
	}
)

// Set sets the last time that a disk space lookup was performed.
func (ult *usageLookupTime) Set(t time.Time) {
	ult.value.Store(&t)
}

// Get the last time that we performed a disk space usage lookup.
func (ult *usageLookupTime) Get() time.Time {
	if pt := ult.value.Load(); pt != nil {
		return *pt
	}
	return time.Time{}
}

// MaxDisk returns the maximum amount of disk space that this Filesystem
// instance is allowed to use.
func (fs *Filesystem) MaxDisk() int64 {
	return fs.diskLimit.Load()
}

// SetDiskLimit sets the disk space limit for this Filesystem instance.
func (fs *Filesystem) SetDiskLimit(i int64) {
	fs.diskLimit.Store(i)
}

// HasSpaceErr returns an error when the filesystem has no space available.
// no space, rather than a boolean value.
func (fs *Filesystem) HasSpaceErr(allowStaleValue bool) error {
	if !fs.HasSpaceAvailable(allowStaleValue) {
		return newFilesystemError(ErrCodeDiskSpace, nil)
	}
	return nil
}

// HasSpaceAvailable reports whether a directory has enough space for a file.
// for the file to be written to.
//
// Because determining the amount of space being used by a server is a taxing operation we
// will load it all up into a cache and pull from that as long as the key is not expired.
//
// This operation will potentially block unless allowStaleValue is set to true. See the
// documentation on DiskUsage for how this affects the call.
func (fs *Filesystem) HasSpaceAvailable(allowStaleValue bool) bool {
	size, err := fs.DiskUsage(allowStaleValue)
	if err != nil {
		log.WithField("root", fs.Path()).WithField("error", err).Warn("failed to determine root fs directory size")
	}

	// If space is -1 or 0 just return true, means they're allowed unlimited.
	//
	// Technically we could skip disk space calculation because we don't need to check if the
	// server exceeds its limit but because this method caches the disk usage it would be best
	// to calculate the disk usage and always return true.
	if fs.MaxDisk() == 0 {
		return true
	}

	return size <= fs.MaxDisk()
}

// CachedUsage returns the cached disk usage. Do not rely on this
// function for critical logical checks. It should only be used in areas where the actual disk usage
// does not need to be perfect, e.g. API responses for server resource usage.
func (fs *Filesystem) CachedUsage() int64 {
	return fs.diskUsed.Load()
}

// DiskUsage reports total filesystem usage while preferring the cached value to avoid
// as needed without overly taxing the system. This will prioritize the value from the cache to avoid
// excessive IO usage. We will only walk the filesystem and determine the size of the directory if there
// is no longer a cached value.
//
// If "allowStaleValue" is set to true, a stale value MAY be returned to the caller if there is an
// expired cache value AND there is currently another lookup in progress. If there is no cached value but
// no other lookup is in progress, a fresh disk space response will be returned to the caller.
//
// This is primarily to avoid a bunch of I/O operations from piling up on the server, especially on servers
// with a large amount of files.
func (fs *Filesystem) DiskUsage(allowStaleValue bool) (int64, error) {
	// A disk check interval of 0 means this functionality is completely disabled.
	if fs.diskCheckInterval == 0 {
		return 0, nil
	}

	if !fs.lastLookupTime.Get().After(time.Now().Add(time.Second * fs.diskCheckInterval * -1)) {
		// If we are now allowing a stale response go ahead  and perform the lookup and return the fresh
		// value. This is a blocking operation to the calling process.
		if !allowStaleValue {
			return fs.refreshDiskUsage()
		} else if fs.lookupInProgress.CompareAndSwap(false, true) {
			// Otherwise, if we allow a stale value and there isn't a valid item in the cache and we aren't
			// currently performing a lookup, just do the disk usage calculation in the background.
			go func(fs *Filesystem) {
				defer fs.lookupInProgress.Store(false)
				if _, err := fs.refreshDiskUsage(); err != nil {
					log.WithField("root", fs.Path()).WithField("error", err).Warn("failed to update fs disk usage from within routine")
				}
			}(fs)
		}
	}

	// Return the currently cached value back to the calling function.
	return fs.diskUsed.Load(), nil
}

// refreshDiskUsage updates the currently used disk space for a server, unless
// another lookup finished while this one was waiting for it.
func (fs *Filesystem) refreshDiskUsage() (int64, error) {
	fs.lookupMu.Lock()
	defer fs.lookupMu.Unlock()

	if fs.lastLookupTime.Get().After(time.Now().Add(time.Second * fs.diskCheckInterval * -1)) {
		return fs.diskUsed.Load(), nil
	}
	return fs.walkDiskUsage()
}

// Updates the currently used disk space for a server.
func (fs *Filesystem) updateCachedDiskUsage() (int64, error) {
	// Obtain an exclusive lock on this process so that we don't unintentionally run it at the same
	// time as another running process.
	fs.lookupMu.Lock()
	defer fs.lookupMu.Unlock()

	return fs.walkDiskUsage()
}

// walkDiskUsage walks the disk and updates the cached usage. The caller must
// hold lookupMu.
func (fs *Filesystem) walkDiskUsage() (int64, error) {
	// Signal that we're currently updating the disk size so that other calls to the disk checking
	// functions can determine if they should queue up additional calls to this function. Ensure that
	// we always set this back to "false" when this process is done executing.
	fs.lookupInProgress.Store(true)
	defer fs.lookupInProgress.Store(false)

	// Writes are not blocked while walking the directory, so keep track of the space reserved for
	// writes that are in progress at any point during the walk.
	fs.mu.Lock()
	fs.walking = true
	fs.walkReserved = fs.reserved
	fs.mu.Unlock()

	// If there is no size its either because there is no data (in which case running this function
	// will have effectively no impact), or there is nothing in the cache, in which case we need to
	// grab the size of their data directory. This is a taxing operation, so we want to store it in
	// the cache once we've gotten it.
	size, err := fs.DirectorySize("/")

	// Always record the lookup, even if there is an error, so that a persistent error does not
	// cause the disk to be walked on every call.
	fs.lastLookupTime.Set(time.Now())

	// The walk may or may not have seen the data written by writes that are still in progress, so
	// add the space reserved for them. Otherwise, a write that started before the walk could write
	// past the disk limit. Writes that finished during the walk are not added, since the walk has
	// usually already counted them, and counting them twice could make the server appear to be
	// over its limit. Either error is corrected by the next walk.
	fs.mu.Lock()
	usage := size + fs.walkReserved
	if err != nil {
		// A walk that failed part of the way through has only counted some of the files, so
		// never lower the usage because of one.
		usage = max(usage, fs.diskUsed.Load())
	}
	fs.diskUsed.Store(usage)
	fs.walking = false
	fs.mu.Unlock()

	return usage, err
}

// DirectorySize calculates the size of a directory and its descendants.
func (fs *Filesystem) DirectorySize(root string) (int64, error) {
	name := normalize(root)

	var hardLinks []uint64

	var size atomic.Int64
	err := walkDirectoryForSize(fs.root.FS(), name, func(path string, d fsys.DirEntry, err error) error {
		// Files can be removed while the walk is running, which is not an error.
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "walkdir err")
		}

		// Only calculate the size of regular files.
		if !d.Type().IsRegular() {
			return nil
		}

		info, err := lstatForDirectorySize(fs.root, path)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return errors.Wrap(err, "lstat err")
		}

		sysFileInfo, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			// Some filesystems do not expose Unix inode metadata. Count the file
			// normally when hard-link information is unavailable.
			size.Add(info.Size())
			return nil
		}
		if sysFileInfo.Nlink > 1 {
			// Hard links have the same inode number
			if slices.Contains(hardLinks, sysFileInfo.Ino) {
				// Don't add hard links size twice
				return nil
			} else {
				hardLinks = append(hardLinks, sysFileInfo.Ino)
			}
		}

		size.Add(info.Size())
		return nil
	})
	return size.Load(), errors.WrapIf(err, "server/filesystem: directorysize: failed to walk directory")
}

// containsHardLinks reports whether a path contains a regular file with a
// second directory entry elsewhere. Deleting one name must not release quota
// while another name still refers to the same inode.
func (fs *Filesystem) containsHardLinks(root string) bool {
	name := normalize(root)
	if info, err := fs.root.Lstat(name); err == nil && info.Mode().IsRegular() {
		stat, ok := info.Sys().(*syscall.Stat_t)
		return ok && stat.Nlink > 1
	}
	found := false
	_ = walkDirectoryForSize(fs.root.FS(), name, func(path string, entry fsys.DirEntry, err error) error {
		if err != nil || !entry.Type().IsRegular() {
			return nil
		}
		info, err := fs.root.Lstat(path)
		if err != nil {
			return nil
		}
		if stat, ok := info.Sys().(*syscall.Stat_t); ok && stat.Nlink > 1 {
			found = true
			return fsys.SkipAll
		}
		return nil
	})
	return found
}

func (fs *Filesystem) HasSpaceFor(size int64) error {
	limit := fs.diskLimit.Load()
	usage := fs.diskUsed.Load()
	if limit == -1 || (limit > 0 && size > limit-usage) {
		return newFilesystemError(ErrCodeDiskSpace, nil)
	}
	return nil
}

// reserveDisk reserves space for a write that is about to happen, returning an
// error if there is not enough space available. Writes happening at the same time
// therefore cannot each use the space that is left. Every reservation must be
// followed by a call to releaseDisk once the write is finished.
func (fs *Filesystem) reserveDisk(size int64) error {
	if size <= 0 {
		return nil
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	if err := fs.HasSpaceFor(size); err != nil {
		return err
	}
	fs.addDisk(size)
	fs.reserved += size
	if fs.walking {
		fs.walkReserved += size
	}
	return nil
}

// releaseDisk finishes a write that reserved space with reserveDisk, replacing
// the reserved space with the amount the write actually changed the disk usage
// by.
func (fs *Filesystem) releaseDisk(reserved int64, used int64) {
	if reserved < 0 {
		reserved = 0
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.reserved -= reserved
	if fs.walking {
		fs.walkReserved = max(fs.walkReserved-reserved, 0)
	}
	fs.addDisk(used - reserved)
}

func (fs *Filesystem) adjustDisk(size int64) int64 {
	if size == 0 {
		return fs.CachedUsage()
	}

	fs.mu.Lock()
	defer fs.mu.Unlock()

	return fs.addDisk(size)
}

func (fs *Filesystem) addDisk(delta int64) int64 {
	for {
		old := fs.diskUsed.Load()
		var next int64
		if delta < 0 && old <= -delta {
			next = 0
		} else {
			next = old + delta
		}
		if fs.diskUsed.CompareAndSwap(old, next) {
			return next
		}
	}
}
