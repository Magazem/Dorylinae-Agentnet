package capability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Magazem/Dorylinae-Agentnet/internal/testutil"
)

// The test binary doubles as a fake git: with CAPTEST_FAKE_GIT set it records
// its argv and environment and answers like git would (or hangs). The
// variable has no GIT_ prefix, so it survives GitEnv.
const (
	fakeGitMode = "CAPTEST_FAKE_GIT"
	fakeGitOut  = "CAPTEST_FAKE_GIT_OUT"
	// fakeGitExecPath is what the fake prints for --exec-path.
	fakeGitExecPath = "CAPTEST_FAKE_GIT_EXEC_PATH"
	// fakeGitVersion is what the fake prints for --version.
	fakeGitVersion = "CAPTEST_FAKE_GIT_VERSION"
)

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeGitMode); mode != "" {
		os.Exit(fakeGit(mode))
	}
	os.Exit(m.Run())
}

type fakeGitCall struct {
	Args []string `json:"args"`
	Env  []string `json:"env"`
}

func fakeGit(mode string) int {
	if mode == "sleep" {
		time.Sleep(time.Minute)
		return 0
	}
	args := os.Args[1:]
	if out := os.Getenv(fakeGitOut); out != "" {
		b, _ := json.Marshal(fakeGitCall{Args: args, Env: os.Environ()})
		f, err := os.OpenFile(out, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // test-controlled path
		if err != nil {
			return 2
		}
		_, _ = f.Write(append(b, '\n'))
		_ = f.Close()
	}
	switch {
	case slices.Contains(args, "for-each-ref"):
		fmt.Printf("%s\x00commit\x00%s\x00\n", args[len(args)-1], strings.Repeat("a", 40))
	case slices.Contains(args, "--exec-path"):
		fmt.Println(os.Getenv(fakeGitExecPath))
	case slices.Contains(args, "--version"):
		fmt.Println(os.Getenv(fakeGitVersion))
	}
	return 0 // ls-tree: no entries
}

// ---- a temporary repository built with the git binary -------------------

type testRepo struct {
	t   *testing.T
	dir string
	git string
}

func requireGit(t *testing.T) string {
	t.Helper()
	p, err := LookGit()
	if err != nil {
		t.Skip("git is not on PATH: the git serving tests need the git binary (CI has it)")
	}
	return p
}

func newTestRepo(t *testing.T, dir string) *testRepo {
	t.Helper()
	r := &testRepo{t: t, dir: dir, git: requireGit(t)}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	r.run("init", "-q", "-b", "main")
	return r
}

// run runs git in the repository with the user's global config disabled, so
// no autocrlf or hook of the machine changes the fixture.
func (r *testRepo) run(args ...string) string {
	r.t.Helper()
	return r.runIn(nil, args...)
}

func (r *testRepo) runIn(stdin []byte, args ...string) string {
	r.t.Helper()
	cmd := exec.Command(r.git, args...) //nolint:gosec // test helper: LookGit path, fixed arguments
	cmd.Dir = r.dir
	cmd.Env = append(GitEnv(os.Environ()),
		"GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.com",
		"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.com")
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		r.t.Fatalf("git %v: %v: %s", args, err, errb.String())
	}
	return strings.TrimSpace(out.String())
}

func (r *testRepo) write(rel, content string) {
	r.t.Helper()
	p := filepath.Join(r.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		r.t.Fatal(err)
	}
}

// blob stores content and returns its id.
func (r *testRepo) blob(content string) string {
	r.t.Helper()
	return r.runIn([]byte(content), "hash-object", "-w", "--no-filters", "--stdin")
}

// entry adds an index entry that the work tree cannot hold portably (a
// symlink, a gitlink, a name with "*").
func (r *testRepo) entry(mode, oid, path string) {
	r.t.Helper()
	// protectNTFS off: Git for Windows refuses "*" in an index path.
	r.run("-c", "core.protectNTFS=false", "update-index", "--add", "--cacheinfo", mode+","+oid+","+path)
}

func (r *testRepo) commit(msg string) string {
	r.t.Helper()
	r.run("add", "-A")
	r.run("commit", "-q", "--allow-empty", "-m", msg)
	return r.run("rev-parse", "HEAD")
}

func resolvedDir(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// ---- harness ------------------------------------------------------------

func newGitHarness(t *testing.T, be GitBackend) *fetchHarness {
	t.Helper()
	return newFetchHarness(t, nil, func(c *FetchConfig) {
		c.Backends = map[string]Backend{KindGit: be}
	})
}

// issueGit signs and stores an active git.read grant over repo#branch.
func (h *fetchHarness) issueGit(repo, branch, scope string) (Record, json.RawMessage) {
	h.t.Helper()
	now := time.Unix(0, h.clock.Load()).UTC()
	g := Grant{
		V: 1, ID: NewID(), Iss: h.iss, Aud: h.holder, Session: testSession, Action: ActionGitRead,
		Resource: Resource{Kind: KindGit, Label: "repo-ab12", Branch: branch}, Scope: scope,
		Nbf: now.Add(-time.Hour), Exp: now.Add(72 * time.Hour), Sensitive: true,
	}
	tok, err := Sign(h.issPriv, g)
	if err != nil {
		h.t.Fatal(err)
	}
	wire, err := Canonical(tok)
	if err != nil {
		h.t.Fatal(err)
	}
	rec := Record{
		ID: g.ID, Direction: DirectionIssued, Peer: h.holder, Session: testSession, Action: ActionGitRead,
		Label: "repo-ab12", Path: repo, Branch: branch, Scope: scope, Sensitive: true, Nbf: g.Nbf, Exp: g.Exp,
		Token: string(wire),
	}
	tx, err := h.store.DB.BeginTx(context.Background(), nil)
	if err != nil {
		h.t.Fatal(err)
	}
	if err := h.store.InsertActiveTx(context.Background(), tx, rec); err != nil {
		h.t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		h.t.Fatal(err)
	}
	return rec, wire
}

func okOne(t *testing.T, resp []map[string]any) map[string]any {
	t.Helper()
	if e := errOf(resp); e != "" {
		t.Fatalf("error %q", e)
	}
	return resp[0]
}

func entryNames(m map[string]any) map[string]string {
	out := map[string]string{}
	for _, e := range m["entries"].([]any) {
		em := e.(map[string]any)
		out[em["name"].(string)] = em["type"].(string)
	}
	return out
}

// ---- acceptance ----------------------------------------------------------

// fixture builds a repository with, on main: a.txt, dir/b.go, "-dash.txt",
// a symlink entry, a submodule entry and a file literally named "star*.go";
// a branch "other" with secret.txt; and a tag v1 with tagged.txt.
func gitFixture(t *testing.T) (*testRepo, string) {
	t.Helper()
	r := newTestRepo(t, filepath.Join(testutil.TempDir(t), "repo"))
	r.write("a.txt", "alpha")
	r.write("dir/b.go", "package b")
	r.write("starX.go", "not the star")
	r.commit("base")

	r.run("checkout", "-q", "-b", "other")
	r.write("secret.txt", "other branch")
	r.commit("other")
	r.run("checkout", "-q", "main")
	r.run("checkout", "-q", "-b", "tagged")
	r.write("tagged.txt", "tag only")
	r.commit("tagged")
	r.run("tag", "-a", "-m", "v1", "v1")
	r.run("checkout", "-q", "main")
	r.run("branch", "-D", "tagged")

	r.entry("100644", r.blob("dash"), "-dash.txt")
	r.entry("120000", r.blob("../../outside"), "link")
	r.entry("160000", r.run("rev-parse", "HEAD"), "sub")
	r.entry("100644", r.blob("literal star"), "star*.go")
	r.run("commit", "-q", "-m", "specials")
	return r, resolvedDir(t, r.dir)
}

func TestGitListStatReadAtTip(t *testing.T) {
	r, repo := gitFixture(t)
	h := newGitHarness(t, GitBackend{Git: r.git})
	_, tok := h.issueGit(repo, "main", "")
	tip := r.run("rev-parse", "refs/heads/main")

	st := okOne(t, h.call(tok, reqOpts{op: OpStat, path: "a.txt"}))
	e := st["entry"].(map[string]any)
	if e["type"] != "file" || e["size"].(float64) != 5 || e["name"] != "a.txt" || st["commit"] != tip {
		t.Fatalf("stat a.txt = %v", st)
	}
	ls := okOne(t, h.call(tok, reqOpts{op: OpList, path: ""}))
	if ls["commit"] != tip {
		t.Fatalf("list commit = %v, want %s", ls["commit"], tip)
	}
	want := map[string]string{"-dash.txt": "file", "a.txt": "file", "dir": "dir", "link": "symlink",
		"star*.go": "file", "starX.go": "file", "sub": "other"}
	if got := entryNames(ls); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("list = %v, want %v", got, want)
	}
	var order []string
	for _, x := range ls["entries"].([]any) {
		order = append(order, x.(map[string]any)["name"].(string))
	}
	if !slices.IsSorted(order) {
		t.Fatalf("list not in byte order: %v", order)
	}
	if got := entryNames(okOne(t, h.call(tok, reqOpts{op: OpList, path: "dir"}))); fmt.Sprint(got) != "map[b.go:file]" {
		t.Fatalf("list dir = %v", got)
	}
	rd := h.call(tok, readOpts("a.txt"))
	if errOf(rd) != "" || string(readAll(rd)) != "alpha" || rd[0]["commit"] != tip {
		t.Fatalf("read a.txt = %v", rd)
	}
	rd = h.call(tok, reqOpts{op: OpRead, path: "a.txt", offset: 2, length: 2})
	if string(readAll(rd)) != "ph" || rd[0]["size"].(float64) != 5 {
		t.Fatalf("read a.txt[2:4] = %v", rd)
	}

	// A new commit moves the tip: content and commit change.
	r.write("a.txt", "beta!")
	r.run("add", "--", "a.txt") // not -A: the special entries have no work-tree file
	r.run("commit", "-q", "-m", "change")
	next := r.run("rev-parse", "HEAD")
	rd = h.call(tok, readOpts("a.txt"))
	if string(readAll(rd)) != "beta!" || rd[0]["commit"] != next || next == tip {
		t.Fatalf("after commit: read = %q commit %v, want %s", readAll(rd), rd[0]["commit"], next)
	}
	if st := okOne(t, h.call(tok, reqOpts{op: OpStat, path: "dir"})); st["commit"] != next || st["entry"].(map[string]any)["type"] != "dir" {
		t.Fatalf("stat dir = %v", st)
	}
}

func TestGitOtherRefsUnreachable(t *testing.T) {
	r, repo := gitFixture(t)
	h := newGitHarness(t, GitBackend{Git: r.git})
	_, tok := h.issueGit(repo, "main", "")
	for _, p := range []string{"secret.txt", "tagged.txt"} {
		h.expectErr(tok, reqOpts{op: OpStat, path: p}, CodeNotFound)
		h.expectErr(tok, readOpts(p), CodeNotFound)
	}
	// A grant naming a tag or a missing branch as its branch serves nothing:
	// only refs/heads/<branch> resolves.
	_, tagTok := h.issueGit(repo, "v1", "")
	h.expectErr(tagTok, reqOpts{op: OpStat, path: "tagged.txt"}, CodeNotFound)
	h.expectErr(tagTok, reqOpts{op: OpList, path: ""}, CodeNotFound)
	// Deleting the granted branch stops serving.
	_, otherTok := h.issueGit(repo, "other", "")
	if got := string(readAll(h.call(otherTok, readOpts("secret.txt")))); got != "other branch" {
		t.Fatalf("other branch read = %q", got)
	}
	r.run("branch", "-D", "other")
	h.expectErr(otherTok, readOpts("secret.txt"), CodeNotFound)
}

// Review 37 M1: a missing branch must not DWIM to a ref that merely ends in
// refs/heads/<branch>, a symbolic ref must not stand in for another ref, and
// a branch must name a commit.
func TestGitBranchResolvesExactly(t *testing.T) {
	r, repo := gitFixture(t)
	other := r.run("rev-parse", "refs/heads/other")
	r.run("update-ref", "refs/tags/refs/heads/gone", other)
	r.run("update-ref", "refs/remotes/refs/heads/gone2", other)
	r.run("update-ref", "refs/refs/heads/gone3", other)
	r.run("symbolic-ref", "refs/heads/alias", "refs/heads/other")
	// git refuses to point a branch at a non-commit: write the loose refs.
	for name, rev := range map[string]string{"tree": "refs/heads/other^{tree}", "annotated": "refs/tags/v1"} {
		ref := filepath.Join(repo, ".git", "refs", "heads", name)
		if err := os.WriteFile(ref, []byte(r.run("rev-parse", rev)+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	r.run("update-ref", "refs/heads/mainx", other) // shares the prefix "main"

	be := GitBackend{Git: r.git}
	h := newGitHarness(t, be)
	for _, b := range []string{"gone", "gone2", "gone3", "alias", "tree", "annotated", "mai"} {
		if c, err := be.BranchTip(context.Background(), repo, b); FetchCode(err) != CodeNotFound {
			t.Errorf("BranchTip(%q) = %q, %v; want not_found", b, c, err)
		}
		_, tok := h.issueGit(repo, b, "")
		h.expectErr(tok, reqOpts{op: OpStat, path: "secret.txt"}, CodeNotFound)
	}
	if c, err := be.BranchTip(context.Background(), repo, "main"); err != nil || c != r.run("rev-parse", "refs/heads/main") {
		t.Fatalf("BranchTip(main) = %q, %v", c, err)
	}
}

func TestGitExecPathBinary(t *testing.T) {
	dir := testutil.TempDir(t)
	t.Setenv(fakeGitMode, "exec-path")
	t.Setenv(fakeGitExecPath, filepath.ToSlash(dir))
	fake := fakeGitPath(t)
	if got := gitExecPathBinary(fake, "git.exe"); got != "" {
		t.Fatalf("no git.exe in the exec path: got %q", got)
	}
	want := filepath.Join(dir, "git.exe")
	if err := os.WriteFile(want, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := gitExecPathBinary(fake, "git.exe"); got != want {
		t.Fatalf("gitExecPathBinary = %q, want %q", got, want)
	}
	t.Setenv(fakeGitExecPath, "relative/dir")
	if got := gitExecPathBinary(fake, "git.exe"); got != "" {
		t.Fatalf("relative exec path accepted: %q", got)
	}
	if runtime.GOOS == "windows" {
		// Review 37 M2: the resolved git is the real one, not the cmd\ launcher.
		if p, err := LookGit(); err == nil && strings.EqualFold(filepath.Base(filepath.Dir(p)), "cmd") {
			t.Fatalf("LookGit = %q: the launcher, whose child outlives a kill", p)
		}
	}
}

func TestGitSymlinkAndSubmoduleNotServed(t *testing.T) {
	r, repo := gitFixture(t)
	h := newGitHarness(t, GitBackend{Git: r.git})
	_, tok := h.issueGit(repo, "main", "")
	if e := okOne(t, h.call(tok, reqOpts{op: OpStat, path: "link"}))["entry"].(map[string]any); e["type"] != "symlink" || e["size"] != nil {
		t.Fatalf("stat link = %v", e)
	}
	if e := okOne(t, h.call(tok, reqOpts{op: OpStat, path: "sub"}))["entry"].(map[string]any); e["type"] != "other" {
		t.Fatalf("stat sub = %v", e)
	}
	h.expectErr(tok, readOpts("link"), CodeSymlink)
	h.expectErr(tok, readOpts("sub"), CodeNotRegular)
	h.expectErr(tok, readOpts("dir"), CodeNotRegular)
	h.expectErr(tok, reqOpts{op: OpList, path: "sub"}, CodeBadPath)
	h.expectErr(tok, reqOpts{op: OpList, path: "link"}, CodeBadPath)
	h.expectErr(tok, reqOpts{op: OpStat, path: "link/x"}, CodeNotFound)
	h.expectErr(tok, reqOpts{op: OpStat, path: "sub/x"}, CodeNotFound)
}

func TestGitPathsAreLiteral(t *testing.T) {
	r, repo := gitFixture(t)
	h := newGitHarness(t, GitBackend{Git: r.git})
	_, tok := h.issueGit(repo, "main", "")
	// A leading "-" is a path after "--", not an option.
	if got := string(readAll(h.call(tok, readOpts("-dash.txt")))); got != "dash" {
		t.Fatalf("read -dash.txt = %q", got)
	}
	// Pattern characters match literally.
	if got := string(readAll(h.call(tok, readOpts("star*.go")))); got != "literal star" {
		t.Fatalf("read star*.go = %q", got)
	}
	for _, p := range []string{"*.go", "star?.go", "star[X].go", "*", "dir/*", "a.tx?", "A.TXT"} {
		h.advance(time.Second) // stay under 20 operations per second
		h.expectErr(tok, reqOpts{op: OpStat, path: p}, CodeNotFound)
		h.expectErr(tok, readOpts(p), CodeNotFound)
	}
	for _, p := range []string{":(glob)x", ":(glob)*.go", ":/a.txt", "dir/../a.txt", "/a.txt", `dir\b.go`} {
		h.advance(time.Second) // stay under 20 operations per second
		h.expectErr(tok, reqOpts{op: OpStat, path: p}, CodeBadPath)
	}
	for _, p := range []string{".git", ".GIT/config", "dir/.Git"} {
		h.advance(time.Second) // stay under 20 operations per second
		h.expectErr(tok, reqOpts{op: OpStat, path: p}, CodeOutOfScope)
	}
}

func TestGitScopeAndLimits(t *testing.T) {
	r := newTestRepo(t, filepath.Join(testutil.TempDir(t), "repo"))
	r.write("internal/mail/m.go", "mail")
	r.write("internal/mailbox/x.go", "box")
	big := bytes.Repeat([]byte("0123456789abcdef"), 6250) // 100000 bytes
	r.write("big.bin", string(big))
	r.commit("base")
	r.entry("100644", r.blob(strings.Repeat("x", MaxFileBytes+1)), "huge.bin")
	r.run("commit", "-q", "-m", "huge")
	repo := resolvedDir(t, r.dir)
	h := newGitHarness(t, GitBackend{Git: r.git})

	_, scoped := h.issueGit(repo, "main", "internal/mail")
	if got := entryNames(okOne(t, h.call(scoped, reqOpts{op: OpList, path: ""}))); fmt.Sprint(got) != "map[m.go:file]" {
		t.Fatalf("scoped list = %v", got)
	}
	h.expectErr(scoped, reqOpts{op: OpStat, path: "../mailbox/x.go"}, CodeBadPath)
	h.expectErr(scoped, reqOpts{op: OpStat, path: "x.go"}, CodeNotFound)

	_, tok := h.issueGit(repo, "main", "")
	h.expectErr(tok, readOpts("huge.bin"), CodeTooLarge)
	if e := okOne(t, h.call(tok, reqOpts{op: OpStat, path: "huge.bin"}))["entry"].(map[string]any); e["size"].(float64) != MaxFileBytes+1 {
		t.Fatalf("stat huge.bin = %v", e)
	}
	// A read spanning fragments, from an offset, is byte-identical.
	rd := h.call(tok, reqOpts{op: OpRead, path: "big.bin", offset: 1000, length: 70000})
	if errOf(rd) != "" || len(rd) != 3 || !bytes.Equal(readAll(rd), big[1000:71000]) {
		t.Fatalf("fragmented read: %d messages, error %q", len(rd), errOf(rd))
	}
	rd = h.call(tok, reqOpts{op: OpRead, path: "big.bin", offset: 200000, length: 10})
	if errOf(rd) != "" || len(readAll(rd)) != 0 || rd[0]["size"].(float64) != 100000 {
		t.Fatalf("read past the end = %v", rd)
	}
}

// markerScript writes an executable shell script that appends to marker.
func markerScript(t *testing.T, path, marker string) {
	t.Helper()
	body := "#!/bin/sh\necho \"$0 $*\" >> '" + filepath.ToSlash(marker) + "'\nexit 0\n"
	//nolint:gosec // a hook script must be executable
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestGitHostileConfigRunsNothing(t *testing.T) {
	base := testutil.TempDir(t)
	r := newTestRepo(t, filepath.Join(base, "repo"))
	r.write(".gitattributes", "* diff=evil filter=evil merge=evil\n")
	r.write("a.txt", "alpha")
	r.write("lost/gone.txt", "lost object")
	r.commit("base")
	gone := r.run("rev-parse", "HEAD:lost/gone.txt")
	repo := resolvedDir(t, r.dir)

	marker := filepath.Join(base, "marker")
	script := filepath.Join(base, "evil.sh")
	markerScript(t, script, marker)
	hooks := filepath.Join(base, "hooks")
	if err := os.MkdirAll(hooks, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, hk := range []string{"pre-commit", "post-checkout", "reference-transaction", "post-index-change", "pre-auto-gc", "post-rewrite"} {
		markerScript(t, filepath.Join(hooks, hk), marker)
	}
	s := "'" + filepath.ToSlash(script) + "'"
	include := filepath.Join(base, "evil.cfg")
	if err := os.WriteFile(include, []byte("[core]\n\tfsmonitor = "+s+"\n\tpager = "+s+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := fmt.Sprintf(`[core]
	repositoryformatversion = 1
	fsmonitor = %[1]s
	hooksPath = %[2]s
	sshCommand = %[1]s
	pager = %[1]s
	askPass = %[1]s
	alternateRefsCommand = %[1]s
[extensions]
	partialClone = origin
[remote "origin"]
	url = ext::sh %[3]s
	promisor = true
[protocol "ext"]
	allow = always
[protocol]
	allow = always
[diff "evil"]
	command = %[1]s
	textconv = %[1]s
[filter "evil"]
	clean = %[1]s
	smudge = %[1]s
	process = %[1]s
	required = true
[merge "evil"]
	driver = %[1]s
[include]
	path = %[4]s
[includeIf "gitdir:**"]
	path = %[4]s
[safe]
	directory = *
`, s, filepath.ToSlash(hooks), filepath.ToSlash(script), filepath.ToSlash(include))
	if err := os.WriteFile(filepath.Join(repo, ".git", "config"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "info", "attributes"), []byte("* diff=evil filter=evil\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A missing object in a "partial clone": reading it would lazily fetch
	// from the ext:: remote.
	if err := os.Remove(filepath.Join(repo, ".git", "objects", gone[:2], gone[2:])); err != nil {
		t.Fatal(err)
	}

	h := newGitHarness(t, GitBackend{Git: r.git})
	_, tok := h.issueGit(repo, "main", "")
	if got := string(readAll(h.call(tok, readOpts("a.txt")))); got != "alpha" {
		t.Fatalf("read a.txt = %q", got)
	}
	okOne(t, h.call(tok, reqOpts{op: OpList, path: ""}))
	okOne(t, h.call(tok, reqOpts{op: OpStat, path: ".gitattributes"}))
	if e := errOf(h.call(tok, readOpts("lost/gone.txt"))); e == "" {
		t.Fatal("read of a missing object succeeded")
	}
	if b, err := os.ReadFile(marker); err == nil { //nolint:gosec // test path
		t.Fatalf("hostile configuration ran: %s", b)
	}

	// Positive control: the same configuration does run under porcelain, so
	// the absence above is the hardening's doing, not a broken fixture.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.git, "commit", "-q", "--allow-empty", "-m", "control") //nolint:gosec // test
	cmd.Dir = repo
	cmd.WaitDelay = time.Second
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.com",
		"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.com", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if _, serr := os.Stat(marker); serr != nil {
		t.Fatalf("positive control: porcelain commit ran no hook (%v, %s); the fixture is not hostile", err, out)
	}
}

func TestGitInheritedEnvironmentIgnored(t *testing.T) {
	r, repo := gitFixture(t)
	decoy := newTestRepo(t, filepath.Join(testutil.TempDir(t), "decoy"))
	decoy.write("a.txt", "decoy")
	decoy.commit("decoy")
	decoyDir := resolvedDir(t, decoy.dir)

	t.Setenv("GIT_DIR", filepath.Join(decoyDir, ".git"))
	t.Setenv("GIT_WORK_TREE", decoyDir)
	t.Setenv("GIT_OBJECT_DIRECTORY", filepath.Join(decoyDir, ".git", "objects"))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(decoyDir, ".git", "index"))
	// If any of these reached git, it would fail on a malformed key or serve
	// the decoy.
	t.Setenv("GIT_CONFIG_PARAMETERS", "'nosection'='x'")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "nosection")
	t.Setenv("GIT_CONFIG_VALUE_0", "x")
	t.Setenv("GIT_CONFIG", filepath.Join(decoyDir, ".git", "config"))
	t.Setenv("GIT_CEILING_DIRECTORIES", "")

	h := newGitHarness(t, GitBackend{Git: r.git})
	_, tok := h.issueGit(repo, "main", "")
	rd := h.call(tok, readOpts("a.txt"))
	if got := string(readAll(rd)); got != "alpha" || rd[0]["commit"] != r.run("rev-parse", "refs/heads/main") {
		t.Fatalf("read = %q (commit %v): the inherited environment redirected git", got, rd[0]["commit"])
	}
}

func readFakeCalls(t *testing.T, path string) []fakeGitCall {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // test path
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []fakeGitCall
	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)
	for sc.Scan() {
		var c fakeGitCall
		if err := json.Unmarshal(sc.Bytes(), &c); err != nil {
			t.Fatal(err)
		}
		out = append(out, c)
	}
	return out
}

func fakeGitPath(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return exe
}

func TestGitEnvironmentAndArgv(t *testing.T) {
	out := filepath.Join(testutil.TempDir(t), "calls.jsonl")
	repo := testutil.TempDir(t)
	t.Setenv(fakeGitMode, "env")
	t.Setenv(fakeGitOut, out)
	t.Setenv("GIT_DIR", "/elsewhere")
	t.Setenv("GIT_CONFIG_PARAMETERS", "'core.hookspath'='/evil'")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.fsmonitor")
	t.Setenv("GIT_CONFIG_VALUE_0", "/evil")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "0")
	t.Setenv("GIT_TRACE", "1")
	if runtime.GOOS != "windows" {
		// Windows names are case-insensitive: this would overwrite GIT_DIR.
		t.Setenv("git_dir", "/elsewhere2")
	}

	be := GitBackend{Git: fakeGitPath(t)}
	rec := Record{Path: repo, Branch: "main"}
	if _, _, err := be.Stat(context.Background(), rec, "-x.go"); FetchCode(err) != CodeNotFound {
		t.Fatalf("stat through the fake = %v, want not_found", err)
	}
	// The issuance-time command builder shares the environment.
	cmd, cancel := GitCommand(context.Background(), be.Git, "show-ref")
	defer cancel()
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}

	calls := readFakeCalls(t, out)
	if len(calls) != 3 {
		t.Fatalf("%d git calls, want 3 (for-each-ref, ls-tree, show-ref)", len(calls))
	}
	fixed := map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1", "GIT_CONFIG_GLOBAL": os.DevNull, "GIT_TERMINAL_PROMPT": "0",
		"GIT_NO_LAZY_FETCH": "1", "GIT_OPTIONAL_LOCKS": "0", "GIT_LITERAL_PATHSPECS": "1",
		"GIT_NO_REPLACE_OBJECTS": "1", "GIT_ATTR_NOSYSTEM": "1",
	}
	for i, c := range calls {
		got := map[string]string{}
		for _, kv := range c.Env {
			k, v, _ := strings.Cut(kv, "=")
			if strings.HasPrefix(strings.ToUpper(k), "GIT_") {
				if _, dup := got[strings.ToUpper(k)]; dup {
					t.Errorf("call %d: %s set twice", i, k)
				}
				got[strings.ToUpper(k)] = v
			}
		}
		want := maps.Clone(fixed)
		if i < 2 { // served commands also stop discovery above the repository
			want["GIT_CEILING_DIRECTORIES"] = filepath.Dir(repo)
		}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("call %d GIT_* environment = %v, want %v", i, got, want)
		}
	}

	forEachRef, lsTree := calls[0].Args, calls[1].Args
	prefix := append([]string{"-C", repo}, gitHardening()...)
	for _, a := range [][]string{forEachRef, lsTree} {
		if !slices.Equal(a[:len(prefix)], prefix) {
			t.Fatalf("argv %q does not start with -C <repo> and the -c overrides", a)
		}
	}
	if w := []string{"for-each-ref", "--count=1", "--sort=refname",
		"--format=%(refname)%00%(objecttype)%00%(objectname)%00%(symref)", "refs/heads/main"}; !slices.Equal(forEachRef[len(prefix):], w) {
		t.Fatalf("for-each-ref argv %q", forEachRef[len(prefix):])
	}
	if w := []string{"ls-tree", "-z", "--full-tree", "-l", strings.Repeat("a", 40), "--", "-x.go"}; !slices.Equal(lsTree[len(prefix):], w) {
		t.Fatalf("ls-tree argv %q", lsTree[len(prefix):])
	}
}

func TestGitTimeout(t *testing.T) {
	if GitTimeout != 10*time.Second {
		t.Fatalf("GitTimeout = %v, want 10s (grant.md §Serving git)", GitTimeout)
	}
	t.Setenv(fakeGitMode, "sleep")
	be := GitBackend{Git: fakeGitPath(t), Timeout: 300 * time.Millisecond}
	start := time.Now()
	_, _, err := be.Stat(context.Background(), Record{Path: testutil.TempDir(t), Branch: "main"}, "a.txt")
	if FetchCode(err) != CodeIO {
		t.Fatalf("slow git: %v, want io_error", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("slow git held the call for %v", d)
	}
	// The default applies when Timeout is unset.
	cmd, cctx, cancel := GitBackend{Git: "git"}.command(context.Background(), "/r", "x")
	defer cancel()
	dl, ok := cctx.Deadline()
	if !ok || time.Until(dl) > GitTimeout || time.Until(dl) < GitTimeout-time.Second || cmd.WaitDelay == 0 {
		t.Fatalf("default deadline %v (ok %v)", time.Until(dl), ok)
	}
}

func TestGitNoDiscoveryAboveRepository(t *testing.T) {
	base := testutil.TempDir(t)
	outer := newTestRepo(t, filepath.Join(base, "outer"))
	outer.write("inner/a.txt", "outer's")
	outer.commit("outer")
	inner := resolvedDir(t, filepath.Join(outer.dir, "inner"))
	// inner used to be a repository (the grant was issued on it) and its .git
	// is gone: git must not fall back to the enclosing repository.
	h := newGitHarness(t, GitBackend{Git: outer.git})
	_, tok := h.issueGit(inner, "main", "")
	h.expectErr(tok, reqOpts{op: OpStat, path: "a.txt"}, CodeNotFound)
	h.expectErr(tok, reqOpts{op: OpList, path: ""}, CodeNotFound)
}

func TestGitMissingBinaryUnsupported(t *testing.T) {
	_, _, err := GitBackend{}.Stat(context.Background(), Record{Path: testutil.TempDir(t), Branch: "main"}, "a")
	if FetchCode(err) != CodeUnsupported {
		t.Fatalf("no git: %v, want unsupported", err)
	}
}

// D23 (Docs/orchestration/HANDOFF.md §3): a git older than 2.32 makes
// git.read grants refused (unsupported), with a clear reason for status/logs;
// fs serving does not go through GitBackend at all.
func TestGitVersionEnforced(t *testing.T) {
	t.Setenv(fakeGitMode, "version")
	fake := fakeGitPath(t)

	cases := []struct {
		version string
		usable  bool
	}{
		{"git version 2.31.0", false},
		{"git version 2.31.9", false},
		{"git version 2.32.0", true},
		{"git version 2.32.1.windows.1", true},
		{"git version 2.45.1", true},
		{"git version 1.9.5", false},
		{"git version 3.0.0", true},
	}
	for _, c := range cases {
		t.Setenv(fakeGitVersion, c.version)
		reason := gitVersionReason(fake)
		if c.usable && reason != "" {
			t.Errorf("%s: reason %q, want none", c.version, reason)
		}
		if !c.usable && reason == "" {
			t.Errorf("%s: no reason, want one (below 2.32)", c.version)
		}
	}

	t.Setenv(fakeGitVersion, "not a git version at all")
	if reason := gitVersionReason(fake); reason == "" {
		t.Fatal("unparsable --version output accepted")
	}
}

// TestGitVersionCheckedOnce confirms the check runs against the resolved
// binary (via GitCommand/gitVersionReason), not a hardcoded assumption, so a
// fresh git on the test machine (CI has one) is reported usable.
func TestGitVersionCheckedOnce(t *testing.T) {
	realGit, err := LookGit()
	if err != nil {
		t.Skip("git is not on PATH")
	}
	if reason := gitVersionReason(realGit); reason != "" {
		t.Fatalf("the real git on this machine: %q (bump CI's git if this is genuinely too old)", reason)
	}
}

func TestGitEnvStripsCaseInsensitively(t *testing.T) {
	env := GitEnv([]string{"PATH=/bin", "GIT_DIR=/x", "git_dir=/y", "Git_Config_Count=1", "GITHUB_TOKEN=t", "XGIT_DIR=1"})
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, "GIT_DIR") || strings.EqualFold(k, "GIT_CONFIG_COUNT") {
			t.Fatalf("GitEnv kept %q", kv)
		}
	}
	if !slices.Contains(env, "GITHUB_TOKEN=t") || !slices.Contains(env, "XGIT_DIR=1") || !slices.Contains(env, "PATH=/bin") {
		t.Fatalf("GitEnv dropped a non-GIT_ variable: %v", env)
	}
}
