// Package sitefs presents a list of stored files as an fs.FS, reading a file
// only when it is opened: a tree can hold thousands of files, and a render opens
// few of them on most changes.
package sitefs

import (
	"bytes"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"time"
)

const (
	root        = "."
	separator   = "/"
	fileMode    = fs.FileMode(0o444)
	dirModeBits = fs.FileMode(0o555)
)

// Entry is one file of a tree.
type Entry struct {
	Path string
	Size int64
}

// Reader reads one file's bytes, which the caller may then change.
type Reader func(name string) ([]byte, error)

// FS is a tree of files, implementing fs.ReadDirFS, fs.ReadFileFS and fs.StatFS.
type FS struct {
	files map[string]int64
	dirs  map[string][]fs.DirEntry
	read  Reader
}

// New makes a tree from entries, reading each file through read when opened.
func New(entries []Entry, read Reader) *FS {
	f := &FS{files: make(map[string]int64, len(entries)), dirs: map[string][]fs.DirEntry{root: {}}, read: read}
	seen := map[string]bool{}
	for _, e := range entries {
		f.files[e.Path] = e.Size
		f.addFile(e, seen)
	}
	for _, list := range f.dirs {
		slices.SortFunc(list, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	}
	return f
}

func split(name string) (dir, base string) {
	dir, base = path.Split(name)
	dir = strings.TrimSuffix(dir, separator)
	if dir == "" {
		dir = root
	}
	return dir, base
}

// addFile lists a file in its directory and each new directory in its parent.
func (f *FS) addFile(e Entry, seen map[string]bool) {
	dir, name := split(e.Path)
	f.dirs[dir] = append(f.dirs[dir], info{name: name, size: e.Size})
	for dir != root && !seen[dir] {
		seen[dir] = true
		parent, base := split(dir)
		f.dirs[parent] = append(f.dirs[parent], info{name: base, dir: true})
		dir = parent
	}
}

// ReadDir lists one directory.
func (f *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	entries, ok := f.dirs[name]
	if !ok {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	return slices.Clone(entries), nil
}

// Stat describes a file or directory without reading it.
func (f *FS) Stat(name string) (fs.FileInfo, error) {
	if _, ok := f.dirs[name]; ok {
		return info{name: path.Base(name), dir: true}, nil
	}
	size, ok := f.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return info{name: path.Base(name), size: size}, nil
}

// ReadFile reads one file.
func (f *FS) ReadFile(name string) ([]byte, error) {
	if _, ok := f.files[name]; !ok {
		return nil, &fs.PathError{Op: "read", Path: name, Err: fs.ErrNotExist}
	}
	return f.read(name)
}

// Open opens a file or directory.
func (f *FS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	stat, err := f.Stat(name)
	if err != nil {
		return nil, err
	}
	if stat.IsDir() {
		return &openDir{info: stat, entries: f.dirs[name]}, nil
	}
	body, err := f.ReadFile(name)
	if err != nil {
		return nil, err
	}
	return &openFile{info: stat, Reader: bytes.NewReader(body)}, nil
}

// info describes one entry; it is both an fs.FileInfo and an fs.DirEntry.
type info struct {
	name string
	size int64
	dir  bool
}

// Name is the entry's base name.
func (i info) Name() string { return i.name }

// Size is a file's length.
func (i info) Size() int64 { return i.size }

// ModTime is the zero time: a stored file has no meaningful one.
func (info) ModTime() time.Time { return time.Time{} }

// IsDir reports a directory.
func (i info) IsDir() bool { return i.dir }

// Sys is nil: there is no underlying source.
func (info) Sys() any { return nil }

// Mode is read-only.
func (i info) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | dirModeBits
	}
	return fileMode
}

// Type is the mode's type bits.
func (i info) Type() fs.FileMode { return i.Mode().Type() }

// Info is the entry itself.
func (i info) Info() (fs.FileInfo, error) { return i, nil }

type openFile struct {
	*bytes.Reader

	info fs.FileInfo
}

// Stat describes the open file.
func (o *openFile) Stat() (fs.FileInfo, error) { return o.info, nil }

// Close does nothing: the bytes are already held.
func (*openFile) Close() error { return nil }

// openDir is an opened directory, listed from where it was last read to.
type openDir struct {
	info    fs.FileInfo
	entries []fs.DirEntry
	read    int
}

// Stat describes the open directory.
func (o *openDir) Stat() (fs.FileInfo, error) { return o.info, nil }

// Close does nothing.
func (*openDir) Close() error { return nil }

// Read fails, as reading any directory does.
func (o *openDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: o.info.Name(), Err: fs.ErrInvalid}
}

// ReadDir is everything left for n <= 0, and at most n with io.EOF at the end.
func (o *openDir) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := o.entries[o.read:]
	if n > 0 && len(rest) == 0 {
		return nil, io.EOF
	}
	if n > 0 && n < len(rest) {
		rest = rest[:n]
	}
	o.read += len(rest)
	return slices.Clone(rest), nil
}
