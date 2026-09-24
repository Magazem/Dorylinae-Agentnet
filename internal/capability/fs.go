package capability

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Limits of Docs/protocol/grant.md §Serving fs and §Transport.
const (
	// MaxFileBytes is the largest file served (`too_large` above it).
	MaxFileBytes = 8 << 20
	// FragmentBytes is the largest raw payload of one fetch.resp fragment.
	FragmentBytes = 32768
	// MaxReadBytes is the largest `length` of one read (8 fragments).
	MaxReadBytes = 262144
	// MaxListEntries is the most entries in one list response.
	MaxListEntries = 1000
	// maxListScan bounds how many directory entries a list will sort.
	maxListScan = 100000
	// maxListBytes bounds the JSON size of the entries of one list response
	// (a Noise plaintext is at most 65535 bytes).
	maxListBytes = 48 << 10
)

// Fetch error codes (Docs/protocol/grant.md §IPC "New error codes"). The
// token verification reasons (ReasonExpired, ...) are also wire codes.
const (
	CodeBadPath     = "bad_path"
	CodeOutOfScope  = "out_of_scope"
	CodeUnknownGrnt = "unknown_grant"
	CodeRevoked     = "revoked"
	CodeSymlink     = "symlink"
	CodeNotRegular  = "not_regular"
	CodeTooLarge    = "too_large"
	CodeRateLimited = "rate_limited"
	CodeStale       = "stale"
	CodeNotFound    = "not_found"
	// CodeIO is any other failure of the grantor's filesystem or send path.
	CodeIO = "io_error"
	// CodeUnsupported: the daemon does not serve this resource kind yet.
	CodeUnsupported = "unsupported"
)

// FetchError is a failure that becomes a fetch.resp error code. Its text is
// the code only: never a path or an OS error (grant.md §Audit).
type FetchError struct{ Code string }

func (e *FetchError) Error() string { return "capability: fetch: " + e.Code }

func fetchErr(code string) error { return &FetchError{Code: code} }

// FetchCode returns the wire code of err: a FetchError's, a verification
// reason, or CodeIO. It never exposes err.Error().
func FetchCode(err error) string {
	var fe *FetchError
	if errors.As(err, &fe) {
		return fe.Code
	}
	if r := ReasonOf(err); r != "" {
		return r
	}
	return CodeIO
}

// Entry describes one file, directory or other object of a resource.
type Entry struct {
	Name string `json:"name"`
	Type string `json:"type"` // file, dir, symlink, other
	Size *int64 `json:"size,omitempty"`
}

// Fragment is one piece of a read.
type Fragment struct {
	Index  int
	Count  int
	Size   int64  // size of the whole file
	Commit string // git.read only
	Data   []byte // valid only during the call
}

// Backend serves one resource kind. rel is the effective path (scope joined
// with the request path, already validated by ValidScopePath and always inside
// the scope); the backend enforces everything about the resource itself.
type Backend interface {
	Stat(ctx context.Context, rec Record, rel string) (e Entry, commit string, err error)
	// List returns entries sorted by name (byte order) after cursor, at most
	// MaxListEntries and about maxListBytes of JSON; next is "" at the end.
	List(ctx context.Context, rec Record, rel, cursor string) (entries []Entry, next, commit string, err error)
	// Read calls emit for each fragment of [offset, offset+length) in order.
	// emit fails when the grant was revoked meanwhile; Read then stops.
	Read(ctx context.Context, rec Record, rel string, offset int64, length int, emit func(Fragment) error) error
}

// FSBackend serves `fs` resources: regular files and directory listings
// under the grant's resolved directory (Docs/protocol/grant.md §Serving fs).
type FSBackend struct{}

// SplitEffective joins scope and path into the effective segment list.
func SplitEffective(scope, path string) []string {
	var segs []string
	for _, p := range []string{scope, path} {
		if p != "" {
			segs = append(segs, strings.Split(p, "/")...)
		}
	}
	return segs
}

// isGitName reports whether seg names a .git entry as NTFS, APFS or HFS+
// would resolve it: case-insensitively, ignoring invisible format characters.
// Upper-casing rune by rune also catches letters whose upper case is ASCII
// without being a Unicode case fold of it (U+0131 "ı" upper-cases to "I", and
// NTFS compares names through an upcase table; review 34 L2).
func isGitName(seg string) bool {
	clean := strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, seg)
	return strings.EqualFold(clean, ".git") || strings.Map(unicode.ToUpper, clean) == ".GIT"
}

// isShortNameShape reports whether seg looks like a Windows 8.3 alias: a "~"
// followed by a digit ("GIT~1" can name .git).
func isShortNameShape(seg string) bool {
	for i := 0; i+1 < len(seg); i++ {
		if seg[i] == '~' && seg[i+1] >= '0' && seg[i+1] <= '9' {
			return true
		}
	}
	return false
}

func mapOSErr(err error) error {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return fetchErr(CodeNotFound)
	case errors.Is(err, fs.ErrPermission):
		return fetchErr(CodeIO)
	}
	return fetchErr(CodeIO)
}

func rootName(segs []string) string {
	if len(segs) == 0 {
		return "."
	}
	return filepath.FromSlash(strings.Join(segs, "/"))
}

// walk checks every component of segs under root before anything is opened
// and returns the final component's Lstat. Nothing is followed: a symlink or
// an irregular file (a Windows junction or other reparse point) anywhere
// yields `symlink`, except that the final component is returned as is when
// finalLink is true (stat reports it).
func walk(root *os.Root, segs []string, finalLink bool) (fs.FileInfo, error) {
	for _, seg := range segs {
		if isGitName(seg) {
			return nil, fetchErr(CodeOutOfScope)
		}
		if runtime.GOOS == "windows" && isShortNameShape(seg) {
			return nil, fetchErr(CodeBadPath)
		}
	}
	if len(segs) == 0 {
		info, err := root.Lstat(".")
		if err != nil {
			return nil, mapOSErr(err)
		}
		return info, nil
	}
	var info fs.FileInfo
	for i := range segs {
		var err error
		info, err = root.Lstat(rootName(segs[:i+1]))
		if err != nil {
			return nil, mapOSErr(err)
		}
		last := i == len(segs)-1
		if info.Mode()&(fs.ModeSymlink|fs.ModeIrregular) != 0 {
			if last && finalLink {
				return info, nil
			}
			return nil, fetchErr(CodeSymlink)
		}
		if !last && !info.IsDir() {
			return nil, fetchErr(CodeNotFound)
		}
	}
	return info, nil
}

func entryOf(name string, info fs.FileInfo) Entry {
	e := Entry{Name: name}
	m := info.Mode()
	switch {
	case m.IsRegular():
		e.Type = "file"
		sz := info.Size()
		e.Size = &sz
	case m.IsDir():
		e.Type = "dir"
	case m&fs.ModeSymlink != 0:
		e.Type = "symlink"
	default:
		e.Type = "other"
	}
	return e
}

func openRoot(rec Record) (*os.Root, error) {
	if rec.Path == "" {
		return nil, fetchErr(CodeNotFound)
	}
	r, err := os.OpenRoot(rec.Path)
	if err != nil {
		return nil, mapOSErr(err)
	}
	return r, nil
}

// Stat implements Backend.
func (FSBackend) Stat(_ context.Context, rec Record, rel string) (Entry, string, error) {
	root, err := openRoot(rec)
	if err != nil {
		return Entry{}, "", err
	}
	defer func() { _ = root.Close() }()
	segs := SplitEffective("", rel)
	info, err := walk(root, segs, true)
	if err != nil {
		return Entry{}, "", err
	}
	name := ""
	if len(segs) > 0 {
		name = segs[len(segs)-1]
	}
	return entryOf(name, info), "", nil
}

// List implements Backend.
func (FSBackend) List(_ context.Context, rec Record, rel, cursor string) ([]Entry, string, string, error) {
	root, err := openRoot(rec)
	if err != nil {
		return nil, "", "", err
	}
	defer func() { _ = root.Close() }()
	segs := SplitEffective("", rel)
	info, err := walk(root, segs, false)
	if err != nil {
		return nil, "", "", err
	}
	if !info.IsDir() {
		return nil, "", "", fetchErr(CodeBadPath)
	}
	dir, err := root.Open(rootName(segs))
	if err != nil {
		return nil, "", "", mapOSErr(err)
	}
	defer func() { _ = dir.Close() }()
	if di, err := dir.Stat(); err != nil || !di.IsDir() || !os.SameFile(info, di) {
		return nil, "", "", fetchErr(CodeSymlink)
	}
	var names []string
	for {
		des, err := dir.ReadDir(256)
		for _, de := range des {
			n := de.Name()
			if isGitName(n) || !utf8.ValidString(n) {
				continue
			}
			names = append(names, n)
		}
		if len(names) > maxListScan {
			return nil, "", "", fetchErr(CodeTooLarge)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", "", mapOSErr(err)
		}
	}
	sort.Strings(names) // byte order
	start := sort.SearchStrings(names, cursor)
	if cursor != "" && start < len(names) && names[start] == cursor {
		start++
	}
	var out []Entry
	size := 0
	for i := start; i < len(names); i++ {
		n := names[i]
		ei, err := root.Lstat(rootName(append(append([]string(nil), segs...), n)))
		if err != nil {
			continue // vanished since the listing
		}
		e := entryOf(n, ei)
		// The encoded size, not an estimate: JSON escapes a byte of a name into
		// up to 6 (review 34 L1).
		b, err := json.Marshal(e)
		if err != nil {
			return nil, "", "", fetchErr(CodeIO)
		}
		est := len(b) + 1
		if len(out) >= MaxListEntries || (len(out) > 0 && size+est > maxListBytes) {
			return out, out[len(out)-1].Name, "", nil
		}
		size += est
		out = append(out, e)
	}
	return out, "", "", nil
}

// Read implements Backend.
func (FSBackend) Read(ctx context.Context, rec Record, rel string, offset int64, length int, emit func(Fragment) error) error {
	root, err := openRoot(rec)
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()
	segs := SplitEffective("", rel)
	info, err := walk(root, segs, false)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fetchErr(CodeNotRegular)
	}
	if info.Size() > MaxFileBytes {
		return fetchErr(CodeTooLarge)
	}
	f, err := root.OpenFile(rootName(segs), os.O_RDONLY|openNonblock, 0)
	if err != nil {
		return mapOSErr(err)
	}
	defer func() { _ = f.Close() }()
	// Checks on the open handle: what was opened is what was checked, and is a
	// regular file (a swap between the Lstat and the open is caught here).
	hi, err := f.Stat()
	if err != nil {
		return fetchErr(CodeIO)
	}
	if !os.SameFile(info, hi) {
		return fetchErr(CodeSymlink)
	}
	if !hi.Mode().IsRegular() {
		return fetchErr(CodeNotRegular)
	}
	size := hi.Size()
	if size > MaxFileBytes {
		return fetchErr(CodeTooLarge)
	}
	n := int64(0)
	if offset < size {
		n = min(int64(length), size-offset)
	}
	frags := int((n + FragmentBytes - 1) / FragmentBytes)
	if frags == 0 {
		frags = 1
	}
	buf := make([]byte, FragmentBytes)
	for i := 0; i < frags; i++ {
		if err := ctx.Err(); err != nil {
			return fetchErr(CodeIO)
		}
		want := int(min(int64(FragmentBytes), n-int64(i)*FragmentBytes))
		if want < 0 {
			want = 0
		}
		got := 0
		if want > 0 {
			got, err = f.ReadAt(buf[:want], offset+int64(i)*FragmentBytes)
			if got < want {
				return fetchErr(CodeIO) // the file shrank under us
			}
			_ = err
		}
		if err := emit(Fragment{Index: i, Count: frags, Size: size, Data: buf[:got]}); err != nil {
			return err
		}
	}
	return nil
}
