package device

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
)

// ReasonWritableByOthers starts the reason of a scope or run refused because
// its program, or a directory on the program's path, can be changed by
// someone other than this device's user or an administrator
// (Docs/protocol/device.md §Scope, review 40 L11).
const ReasonWritableByOthers = "writable_by_others"

// WritableError is a program that others can swap: Path is the file or
// directory whose owner or permissions allow it, Who says by whom.
type WritableError struct {
	Path string
	Who  string
}

func (e *WritableError) Error() string {
	return fmt.Sprintf("%s: %s can be changed by %s", ReasonWritableByOthers, DisplayQuote(e.Path), e.Who)
}

// pathRole is what a checked path is to the program, which decides the
// rights that count on Windows.
type pathRole int

const (
	roleProgram  pathRole = iota // the file itself
	roleParent                   // the directory holding it
	roleAncestor                 // any directory above that
	roleLink                     // a symbolic link or junction met on the way
)

// maxLinks bounds how many symbolic links CheckProgramOwner follows, like the
// kernel's own limit (ELOOP).
const maxLinks = 40

// CheckProgramOwner is CheckProgramOwnerEnv with the environment of a run
// that names no extra variables (MinimalEnv): PATH is one of BaseEnv, so it
// is the PATH every run gets.
func CheckProgramOwner(path string) error {
	return CheckProgramOwnerEnv(path, MinimalEnv(nil, nil))
}

// CheckProgramOwnerEnv refuses a program that users other than this device's
// user (or root / Administrators, SYSTEM, TrustedInstaller) can change: the
// file, the directory holding it and every directory above it, along the
// path as given and, for every symbolic link met on the way, along the
// link's target too. So a link that sits in a directory others can write is
// caught even when that directory is neither on the given nor on the fully
// resolved path (review 40 L11, review 41 M1). The content is not pinned, so
// a toolchain upgrade made by the same user or an administrator keeps
// working.
//
// On Unix a script's "#!" interpreter runs it, so the interpreter is checked
// the same way, and so is its own, if it is a script too (review 55
// R55-097). An interpreter that is not an absolute path is refused. For
// "#!/usr/bin/env X" (or "env -S X ..."), env is checked, X is found as env
// finds it, on the PATH of env, the environment the run gets, and the
// program found is checked like an interpreter, its own "#!" chain included
// (D71). Every existing directory searched before it is checked too, so no
// one else can put another X there. X is refused when it is not found, or
// when a relative directory (the run's working directory) is searched first.
func CheckProgramOwnerEnv(path string, env []string) error {
	w := ownerWalk{seen: map[string]bool{}}
	via := ""
	for n := 0; ; n++ {
		if err := w.checkProgram(path); err != nil {
			if via != "" {
				return fmt.Errorf("%w (%s)", err, via)
			}
			return err
		}
		if runtime.GOOS == "windows" {
			return nil // only an .exe or .com is accepted, never a script
		}
		interp, prog, err := scriptInterpreter(path)
		if err != nil || interp == "" {
			return err
		}
		if n == maxInterpreters {
			return errors.New("device: check the program: too many nested #! interpreters")
		}
		if prog == "" {
			path, via = interp, ""
			continue
		}
		// interp is env: it runs prog, found on the run's PATH (D71).
		if err := w.checkProgram(interp); err != nil {
			return err
		}
		if inner, _, err := scriptInterpreter(interp); err != nil {
			return err
		} else if inner != "" {
			return fmt.Errorf("device: the script's #! interpreter %s is itself a script", DisplayQuote(interp))
		}
		found, err := w.lookPath(prog, env)
		if err != nil {
			return err
		}
		path = found
		via = fmt.Sprintf("the program the script's #! env runs as %s, found on the run's PATH at %s", DisplayQuote(prog), DisplayQuote(found))
	}
}

// checkProgram checks the program or interpreter at p, which must be
// absolute and must exist.
func (w *ownerWalk) checkProgram(p string) error {
	if !filepath.IsAbs(p) {
		return errors.New("device: the program path is not absolute")
	}
	if _, err := filepath.EvalSymlinks(p); err != nil {
		return fmt.Errorf("device: resolve the program: %w", err)
	}
	return w.check(filepath.Clean(p), 0)
}

// lookPath finds prog, which a "#!" line runs through env, as env does: an
// absolute prog as it is, otherwise in the first directory on env's PATH
// that holds an executable regular file of that name. Every existing
// directory searched before it is checked like the program's own
// directory, so that no one else can put another prog there.
func (w *ownerWalk) lookPath(prog string, env []string) (string, error) {
	if filepath.IsAbs(prog) {
		return prog, nil
	}
	list, ok := envValue(env, "PATH")
	if !ok {
		return "", fmt.Errorf("device: the script's #! env runs %s, but the run has no PATH to find it on", DisplayQuote(prog))
	}
	for _, dir := range filepath.SplitList(list) {
		if !filepath.IsAbs(dir) {
			return "", fmt.Errorf("device: the script's #! env runs %s, but the run's PATH searches the relative directory %s (the run's working directory) first", DisplayQuote(prog), DisplayQuote(dir))
		}
		dir = filepath.Clean(dir)
		p := filepath.Join(dir, prog)
		if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0 { //nolint:gosec // a PATH entry of the run, checked next
			return p, nil
		}
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() { //nolint:gosec // a PATH entry of the run, being checked
			if err := w.check(dir, 1); err != nil {
				return "", fmt.Errorf("%w (searched on the run's PATH for %s, which the script's #! env runs)", err, DisplayQuote(prog))
			}
		}
	}
	return "", fmt.Errorf("device: the script's #! env runs %s, which is not found on the run's PATH", DisplayQuote(prog))
}

// envValue returns the value of name in env, the last one if it is set more
// than once (as os/exec does).
func envValue(env []string, name string) (string, bool) {
	v, ok := "", false
	for _, kv := range env {
		if k, val, found := strings.Cut(kv, "="); found && k == name {
			v, ok = val, true
		}
	}
	return v, ok
}

// maxInterpreters bounds how many nested "#!" interpreters CheckProgramOwner
// follows (Linux itself allows only a few).
const maxInterpreters = 4

// shebangMax is how much of a file's start the kernel reads for its "#!"
// line (Linux BINPRM_BUF_SIZE).
const shebangMax = 256

// scriptInterpreter returns the "#!" interpreter of the file at path and,
// when it is env, the program env runs (parseShebang). Both are "" when the
// file is not a "#!" script, is not a regular file (never read: a FIFO
// would block), or cannot be read: the kernel runs a binary that is
// executable but not readable (mode 0711), while a script that cannot be
// read fails in its interpreter (review 86 L1).
func scriptInterpreter(path string) (interp, prog string, err error) {
	fi, err := os.Stat(path) //nolint:gosec // the program being checked; its ownership was checked above
	if err != nil {
		return "", "", fmt.Errorf("device: check the program: %w", err)
	}
	if !fi.Mode().IsRegular() {
		return "", "", nil
	}
	f, err := os.Open(path) //nolint:gosec // the program being checked; its ownership was checked above
	if errors.Is(err, fs.ErrPermission) {
		return "", "", nil
	}
	if err != nil {
		return "", "", fmt.Errorf("device: check the program: %w", err)
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, shebangMax)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", "", fmt.Errorf("device: check the program: %w", err)
	}
	return parseShebang(buf[:n])
}

// parseShebang returns the interpreter named by a "#!" line at the start of
// head, "" when head does not start with "#!", or an error for an
// interpreter that cannot be checked. When the interpreter is env, prog is
// the program it runs, from "env X" or "env -S X ..."; any other env option,
// a variable env would set first (PATH=... would change where X is found),
// and a quote, escape or ${...} that -S would expand are refused.
func parseShebang(head []byte) (interp, prog string, err error) {
	line, ok := bytes.CutPrefix(head, []byte("#!"))
	if !ok {
		return "", "", nil
	}
	if i := bytes.IndexByte(line, '\n'); i >= 0 {
		line = line[:i]
	}
	fields := strings.Fields(string(line))
	if len(fields) == 0 {
		return "", "", errors.New("device: the script's #! line names no interpreter")
	}
	interp = fields[0]
	if !strings.HasPrefix(interp, "/") {
		return "", "", fmt.Errorf("device: the script's #! interpreter %s is not an absolute path", DisplayQuote(interp))
	}
	if path.Base(interp) != "env" {
		return interp, "", nil
	}
	rest := fields[1:]
	if len(rest) > 0 && (rest[0] == "-S" || rest[0] == "--split-string") {
		rest = rest[1:]
	}
	if len(rest) == 0 {
		return "", "", fmt.Errorf("device: the script's #! interpreter %s names no program to run", DisplayQuote(interp))
	}
	prog = rest[0]
	switch {
	case strings.HasPrefix(prog, "-"):
		return "", "", fmt.Errorf("device: the script's #! line passes %s the option %s, which cannot be checked; use env X or env -S X", DisplayQuote(interp), DisplayQuote(prog))
	case strings.Contains(prog, "="):
		return "", "", fmt.Errorf("device: the script's #! line has %s set %s before its program, which cannot be checked", DisplayQuote(interp), DisplayQuote(prog))
	case strings.ContainsAny(prog, `$\"'`):
		return "", "", fmt.Errorf("device: the script's #! line names the program %s, which env would expand and cannot be checked", DisplayQuote(prog))
	case strings.Contains(prog, "/") && !strings.HasPrefix(prog, "/"):
		return "", "", fmt.Errorf("device: the script's #! line runs %s through env, which is not an absolute path", DisplayQuote(prog))
	}
	return interp, prog, nil
}

// ownerWalk is one CheckProgramOwner: what was checked, and how many links
// were followed.
type ownerWalk struct {
	seen  map[string]bool
	links int
}

// check checks p and every directory above it; depth is p's distance from
// the program (0: the program, 1: its directory), which sets the role. A
// symbolic link (or, on Windows, a junction) on the way is checked as a link
// and followed: its target is checked the same way, at the link's depth.
func (w *ownerWalk) check(p string, depth int) error {
	for i, q := range pathChain(p) {
		role := roleAncestor
		switch depth + i {
		case 0:
			role = roleProgram
		case 1:
			role = roleParent
		}
		target, isLink, err := linkTarget(q)
		if err != nil {
			return err
		}
		if isLink {
			role = roleLink
		}
		if key := fmt.Sprint(int(role), ":", q); !w.seen[key] {
			w.seen[key] = true
			if err := checkPathOwner(q, role); err != nil {
				return err
			}
		}
		if !isLink {
			continue
		}
		if w.links++; w.links > maxLinks {
			return errors.New("device: check the program's path: too many symbolic links")
		}
		if !filepath.IsAbs(target) {
			target = filepath.Join(filepath.Dir(q), target)
		}
		if err := w.check(filepath.Clean(target), depth+i); err != nil {
			return err
		}
	}
	return nil
}

// linkTarget reports whether p is a symbolic link (or, on Windows, a
// junction) and, if so, what it points to.
func linkTarget(p string) (string, bool, error) {
	fi, err := os.Lstat(p)
	if err != nil {
		return "", false, fmt.Errorf("device: check the program's path: %w", err)
	}
	switch {
	case fi.Mode()&os.ModeSymlink != 0:
		t, err := os.Readlink(p)
		if err != nil {
			return "", false, fmt.Errorf("device: check the program's path: %w", err)
		}
		return t, true, nil
	case runtime.GOOS == "windows" && fi.Mode()&os.ModeIrregular != 0:
		// A junction (mount point) is irregular, and so are reparse points
		// that are not links (dedup, cloud files): only a junction reads
		// back as a target; the others are checked as they are.
		if t, err := os.Readlink(p); err == nil {
			return t, true, nil
		}
	}
	return "", false, nil
}

// pathChain returns p and every directory above it, up to the root.
func pathChain(p string) []string {
	out := []string{p}
	for {
		d := filepath.Dir(p)
		if d == p {
			return out
		}
		out = append(out, d)
		p = d
	}
}
