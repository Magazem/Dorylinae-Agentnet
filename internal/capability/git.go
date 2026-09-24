package capability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// GitTimeout bounds every git call (Docs/protocol/grant.md §Serving git).
const GitTimeout = 10 * time.Second

const (
	// gitWaitDelay bounds how long Wait waits for the output pipes after git
	// was killed (a grandchild holding them must not hang a worker).
	gitWaitDelay = time.Second
	// maxLsTreeBytes bounds the output of one ls-tree call read into memory.
	maxLsTreeBytes = 32 << 20
)

// gitFixedEnv is added to the environment of every git call once every
// inherited GIT_* variable is removed (Docs/protocol/grant.md §Serving git).
func gitFixedEnv() []string {
	return []string{
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=" + os.DevNull,
		"GIT_TERMINAL_PROMPT=0",
		"GIT_NO_LAZY_FETCH=1",
		"GIT_OPTIONAL_LOCKS=0",
		"GIT_LITERAL_PATHSPECS=1",
		"GIT_NO_REPLACE_OBJECTS=1",
		"GIT_ATTR_NOSYSTEM=1",
	}
}

// GitEnv returns environ without any GIT_* variable (compared without case:
// Windows environment names are case-insensitive) and with the fixed
// variables of Docs/protocol/grant.md §Serving git added.
func GitEnv(environ []string) []string {
	env := make([]string, 0, len(environ)+8)
	for _, kv := range environ {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, gitFixedEnv()...)
}

// LookGit resolves the git executable to an absolute path. The daemon calls
// it once at start; requests never search PATH. On Windows, PATH usually
// holds Git for Windows' cmd\git.exe, a launcher that runs the real git as a
// child: killing the launcher on timeout leaves that child running (review
// 37 M2), so the git.exe of `git --exec-path` is used instead.
func LookGit() (string, error) {
	p, err := exec.LookPath("git")
	if err != nil {
		return "", err
	}
	p, err = filepath.Abs(p)
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "windows" {
		if bin := gitExecPathBinary(p, "git.exe"); bin != "" {
			return bin, nil
		}
	}
	return p, nil
}

// gitExecPathBinary returns <git --exec-path>/<name> if it is a regular file,
// else "".
func gitExecPathBinary(gitPath, name string) string {
	cmd, cancel := GitCommand(context.Background(), gitPath, "--exec-path")
	defer cancel()
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	dir := filepath.FromSlash(strings.TrimSpace(string(out)))
	if !filepath.IsAbs(dir) {
		return ""
	}
	bin := filepath.Join(dir, name)
	if fi, err := os.Stat(bin); err != nil || !fi.Mode().IsRegular() {
		return ""
	}
	return bin
}

// GitCommand builds a git command with no shell, a GitTimeout deadline and
// the environment of GitEnv. gitPath must be absolute (LookGit); args must be
// fixed flags and validated values, with any peer-supplied path after "--".
func GitCommand(ctx context.Context, gitPath string, args ...string) (*exec.Cmd, context.CancelFunc) {
	cmd, _, cancel := gitCommandTimeout(ctx, GitTimeout, gitPath, args...)
	return cmd, cancel
}

// gitCommandTimeout is GitCommand with a timeout d; it also returns the
// command's context, whose error tells a timeout from a git failure.
func gitCommandTimeout(ctx context.Context, d time.Duration, gitPath string, args ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(ctx, d)
	//nolint:gosec // no shell; gitPath is resolved once by LookGit and every
	// caller passes fixed flags and validated values, peer paths after "--".
	cmd := exec.CommandContext(ctx, gitPath, args...)
	cmd.Env = GitEnv(os.Environ())
	cmd.WaitDelay = gitWaitDelay
	return cmd, ctx, cancel
}

// gitHardening are the -c overrides put before every served command. The
// command line has the highest configuration priority, so a hostile
// .git/config (or a file it includes) cannot turn these back on. The
// commands served (for-each-ref, ls-tree, cat-file blob) run no hooks, filters
// or diff drivers anyway; these close the remaining paths to a subprocess:
// fsmonitor, hooks and any transport (a lazy fetch of a partial clone).
func gitHardening() []string {
	a := []string{
		"-c", "core.fsmonitor=false",
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "protocol.allow=never",
	}
	for _, p := range []string{"ext", "file", "git", "ssh", "http", "https"} {
		a = append(a, "-c", "protocol."+p+".allow=never")
	}
	return a
}

// GitBackend serves `git` resources: tree listings and blobs of
// refs/heads/<branch> at its tip (Docs/protocol/grant.md §Serving git). The
// tip is resolved on every call and reported as `commit`.
type GitBackend struct {
	// Git is the absolute path of the git executable, resolved once at start
	// (LookGit). Empty: every call fails with `unsupported`.
	Git string
	// Timeout bounds each git call; 0 = GitTimeout.
	Timeout time.Duration
}

// NewGitBackend resolves git once. Without git the backend answers
// `unsupported`.
func NewGitBackend() GitBackend {
	p, err := LookGit()
	if err != nil {
		return GitBackend{}
	}
	return GitBackend{Git: p}
}

type gitEntry struct {
	mode string
	typ  string
	oid  string
	size int64 // -1 when not a blob
	path string
}

// command builds a hardened git command on repo. Discovery stops at repo:
// if repo stopped being a repository, git must not serve an enclosing one.
func (b GitBackend) command(ctx context.Context, repo string, args ...string) (*exec.Cmd, context.Context, context.CancelFunc) {
	d := b.Timeout
	if d <= 0 {
		d = GitTimeout
	}
	full := append([]string{"-C", repo}, gitHardening()...)
	full = append(full, args...)
	cmd, cctx, cancel := gitCommandTimeout(ctx, d, b.Git, full...)
	cmd.Env = append(cmd.Env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(repo))
	return cmd, cctx, cancel
}

func (b GitBackend) check(rec Record) error {
	if b.Git == "" {
		return fetchErr(CodeUnsupported)
	}
	if rec.Path == "" || !filepath.IsAbs(rec.Path) || rec.Branch == "" || checkBranch(rec.Branch) != nil {
		return fetchErr(CodeNotFound)
	}
	return nil
}

func (b GitBackend) tip(ctx context.Context, rec Record) (string, error) {
	return b.BranchTip(ctx, rec.Path, rec.Branch)
}

// BranchTip resolves refs/heads/<branch> of repo to the commit it names,
// exactly. for-each-ref matches the full ref name only: rev-parse would DWIM
// a missing branch to refs/tags/refs/heads/<branch> or
// refs/remotes/refs/heads/<branch> (review 37 M1). A symbolic ref (which
// could name any other ref) and a ref to anything but a commit are
// not_found. Issuance runs the same check.
func (b GitBackend) BranchTip(ctx context.Context, repo, branch string) (string, error) {
	if b.Git == "" {
		return "", fetchErr(CodeUnsupported)
	}
	if !filepath.IsAbs(repo) || checkBranch(branch) != nil {
		return "", fetchErr(CodeNotFound)
	}
	ref := "refs/heads/" + branch
	// Sorted by name, the exact ref comes before any refs/heads/<branch>/...
	cmd, cctx, cancel := b.command(ctx, repo, "for-each-ref", "--count=1", "--sort=refname",
		"--format=%(refname)%00%(objecttype)%00%(objectname)%00%(symref)", ref)
	defer cancel()
	var out bytes.Buffer
	cmd.Stdout = &limitedWriter{w: &out, n: 2048}
	if err := cmd.Run(); err != nil {
		if cctx.Err() != nil {
			return "", fetchErr(CodeIO) // timed out or cancelled
		}
		return "", fetchErr(CodeNotFound) // no longer a repository
	}
	line := strings.TrimSuffix(out.String(), "\n")
	if line == "" {
		return "", fetchErr(CodeNotFound) // the branch is gone
	}
	f := strings.Split(line, "\x00")
	if len(f) != 4 {
		return "", fetchErr(CodeIO)
	}
	if f[0] != ref || f[1] != "commit" || f[3] != "" {
		return "", fetchErr(CodeNotFound)
	}
	if !isHexOID(f[2]) {
		return "", fetchErr(CodeIO)
	}
	return f[2], nil
}

func isHexOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// lsTree runs `ls-tree -z --full-tree -l <commit> [-- <path>]` and parses
// its records. It fails with `too_large` past maxListScan records.
func (b GitBackend) lsTree(ctx context.Context, rec Record, commit string, path string) ([]gitEntry, error) {
	args := []string{"ls-tree", "-z", "--full-tree", "-l", commit}
	if path != "" {
		args = append(args, "--", path)
	}
	cmd, _, cancel := b.command(ctx, rec.Path, args...)
	defer cancel()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fetchErr(CodeIO)
	}
	if err := cmd.Start(); err != nil {
		return nil, fetchErr(CodeIO)
	}
	var out []gitEntry
	var perr error
	r := bufio.NewReader(io.LimitReader(stdout, maxLsTreeBytes+1))
	total := 0
	for {
		line, err := r.ReadSlice(0)
		if errors.Is(err, bufio.ErrBufferFull) {
			// A record longer than the reader's buffer: gather it.
			buf := append([]byte(nil), line...)
			for errors.Is(err, bufio.ErrBufferFull) {
				line, err = r.ReadSlice(0)
				buf = append(buf, line...)
			}
			line = buf
		}
		total += len(line)
		if total > maxLsTreeBytes {
			perr = fetchErr(CodeTooLarge)
			break
		}
		if len(line) > 0 && err == nil {
			e, ok := parseLsTree(line[:len(line)-1])
			if !ok {
				perr = fetchErr(CodeIO)
				break
			}
			out = append(out, e)
			if len(out) > maxListScan {
				perr = fetchErr(CodeTooLarge)
				break
			}
		}
		if err != nil {
			if !errors.Is(err, io.EOF) || len(line) > 0 {
				perr = fetchErr(CodeIO) // a read error or an unterminated record
			}
			break
		}
	}
	if perr != nil {
		cancel()
		_ = cmd.Wait()
		return nil, perr
	}
	if err := cmd.Wait(); err != nil {
		// A timeout, or a failure on a commit rev-parse just resolved (a
		// corrupt or vanished repository).
		return nil, fetchErr(CodeIO)
	}
	return out, nil
}

// parseLsTree parses "<mode> SP <type> SP <oid> SP+ <size|-> TAB <path>".
func parseLsTree(rec []byte) (gitEntry, bool) {
	tab := bytes.IndexByte(rec, '\t')
	if tab < 0 {
		return gitEntry{}, false
	}
	f := strings.Fields(string(rec[:tab]))
	if len(f) != 4 || !isHexOID(f[2]) {
		return gitEntry{}, false
	}
	e := gitEntry{mode: f[0], typ: f[1], oid: f[2], size: -1, path: string(rec[tab+1:])}
	if f[3] != "-" {
		n, err := strconv.ParseInt(f[3], 10, 64)
		if err != nil || n < 0 {
			return gitEntry{}, false
		}
		e.size = n
	}
	return e, true
}

// entryOfGit maps a tree entry to a fetch entry. Only 100644/100755 blobs are
// files; 120000 is a symlink and 160000 (a submodule) or anything else is
// other, and neither is ever served.
func entryOfGit(name string, e gitEntry) Entry {
	out := Entry{Name: name}
	switch {
	case e.typ == "blob" && (e.mode == "100644" || e.mode == "100755"):
		out.Type = "file"
		sz := e.size
		out.Size = &sz
	case e.typ == "tree" && e.mode == "040000":
		out.Type = "dir"
	case e.typ == "blob" && e.mode == "120000":
		out.Type = "symlink"
	default:
		out.Type = "other"
	}
	return out
}

// gitSegments checks rel for git serving: a `.git` component (any case) is
// out of scope, as for fs, so the two kinds refuse the same names.
func gitSegments(rel string) ([]string, error) {
	segs := SplitEffective("", rel)
	for _, s := range segs {
		if isGitName(s) {
			return nil, fetchErr(CodeOutOfScope)
		}
	}
	return segs, nil
}

// stat resolves rel at commit to exactly one tree entry.
func (b GitBackend) stat(ctx context.Context, rec Record, commit, rel string) (gitEntry, error) {
	entries, err := b.lsTree(ctx, rec, commit, rel)
	if err != nil {
		return gitEntry{}, err
	}
	for _, e := range entries {
		if e.path == rel { // exact bytes: no case folding, no pattern
			return e, nil
		}
	}
	return gitEntry{}, fetchErr(CodeNotFound)
}

// Stat implements Backend.
func (b GitBackend) Stat(ctx context.Context, rec Record, rel string) (Entry, string, error) {
	if err := b.check(rec); err != nil {
		return Entry{}, "", err
	}
	segs, err := gitSegments(rel)
	if err != nil {
		return Entry{}, "", err
	}
	commit, err := b.tip(ctx, rec)
	if err != nil {
		return Entry{}, "", err
	}
	if len(segs) == 0 {
		return Entry{Name: "", Type: "dir"}, commit, nil
	}
	e, err := b.stat(ctx, rec, commit, rel)
	if err != nil {
		return Entry{}, "", err
	}
	return entryOfGit(segs[len(segs)-1], e), commit, nil
}

// List implements Backend.
func (b GitBackend) List(ctx context.Context, rec Record, rel, cursor string) ([]Entry, string, string, error) {
	if err := b.check(rec); err != nil {
		return nil, "", "", err
	}
	if _, err := gitSegments(rel); err != nil {
		return nil, "", "", err
	}
	commit, err := b.tip(ctx, rec)
	if err != nil {
		return nil, "", "", err
	}
	prefix := ""
	if rel != "" {
		e, err := b.stat(ctx, rec, commit, rel)
		if err != nil {
			return nil, "", "", err
		}
		if e.typ != "tree" || e.mode != "040000" {
			return nil, "", "", fetchErr(CodeBadPath)
		}
		prefix = rel + "/"
	}
	raw, err := b.lsTree(ctx, rec, commit, prefix)
	if err != nil {
		return nil, "", "", err
	}
	byName := make(map[string]gitEntry, len(raw))
	names := make([]string, 0, len(raw))
	for _, e := range raw {
		name, ok := strings.CutPrefix(e.path, prefix)
		if !ok || name == "" || strings.Contains(name, "/") || isGitName(name) || !utf8.ValidString(name) {
			continue
		}
		if _, dup := byName[name]; dup {
			continue
		}
		byName[name] = e
		names = append(names, name)
	}
	sort.Strings(names) // byte order (git sorts a tree "name" as "name/")
	start := sort.SearchStrings(names, cursor)
	if cursor != "" && start < len(names) && names[start] == cursor {
		start++
	}
	var out []Entry
	size := 0
	for i := start; i < len(names); i++ {
		e := entryOfGit(names[i], byName[names[i]])
		enc, err := json.Marshal(e)
		if err != nil {
			return nil, "", "", fetchErr(CodeIO)
		}
		est := len(enc) + 1
		if len(out) >= MaxListEntries || (len(out) > 0 && size+est > maxListBytes) {
			return out, out[len(out)-1].Name, commit, nil
		}
		size += est
		out = append(out, e)
	}
	return out, "", commit, nil
}

// Read implements Backend. The blob is streamed from `cat-file blob`, never
// held whole in memory.
func (b GitBackend) Read(ctx context.Context, rec Record, rel string, offset int64, length int, emit func(Fragment) error) error {
	if err := b.check(rec); err != nil {
		return err
	}
	segs, err := gitSegments(rel)
	if err != nil {
		return err
	}
	if len(segs) == 0 {
		return fetchErr(CodeNotRegular)
	}
	commit, err := b.tip(ctx, rec)
	if err != nil {
		return err
	}
	e, err := b.stat(ctx, rec, commit, rel)
	if err != nil {
		return err
	}
	switch {
	case e.typ == "blob" && (e.mode == "100644" || e.mode == "100755"):
	case e.typ == "blob" && e.mode == "120000":
		return fetchErr(CodeSymlink)
	default:
		return fetchErr(CodeNotRegular)
	}
	size := e.size
	if size < 0 {
		return fetchErr(CodeIO)
	}
	if size > MaxFileBytes {
		return fetchErr(CodeTooLarge)
	}

	cmd, _, cancel := b.command(ctx, rec.Path, "cat-file", "blob", e.oid)
	defer cancel()
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fetchErr(CodeIO)
	}
	if err := cmd.Start(); err != nil {
		return fetchErr(CodeIO)
	}
	stop := func() {
		cancel()
		_ = cmd.Wait()
	}
	src := io.LimitReader(stdout, size)
	n := int64(0)
	if offset < size {
		n = min(int64(length), size-offset)
		if _, err := io.CopyN(io.Discard, src, offset); err != nil {
			stop()
			return fetchErr(CodeIO)
		}
	}
	frags := int((n + FragmentBytes - 1) / FragmentBytes)
	if frags == 0 {
		frags = 1
	}
	buf := make([]byte, FragmentBytes)
	for i := 0; i < frags; i++ {
		if err := ctx.Err(); err != nil {
			stop()
			return fetchErr(CodeIO)
		}
		want := int(min(int64(FragmentBytes), n-int64(i)*FragmentBytes))
		if want < 0 {
			want = 0
		}
		if want > 0 {
			if _, err := io.ReadFull(src, buf[:want]); err != nil {
				stop()
				return fetchErr(CodeIO)
			}
		}
		if err := emit(Fragment{Index: i, Count: frags, Size: size, Commit: commit, Data: buf[:want]}); err != nil {
			stop()
			return err
		}
	}
	// The rest of the blob is not needed: stop git rather than drain it.
	stop()
	return nil
}

// limitedWriter keeps at most n bytes and drops the rest.
type limitedWriter struct {
	w io.Writer
	n int
}

func (l *limitedWriter) Write(p []byte) (int, error) {
	k := len(p)
	if l.n > 0 {
		q := p
		if len(q) > l.n {
			q = q[:l.n]
		}
		l.n -= len(q)
		_, _ = l.w.Write(q)
	}
	return k, nil
}
