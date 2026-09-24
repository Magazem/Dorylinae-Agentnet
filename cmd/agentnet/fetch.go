package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/Magazem/Dorylinae-Agentnet/internal/capability"
	"github.com/Magazem/Dorylinae-Agentnet/internal/daemon"
)

const (
	defaultFetchTimeoutSec = 30
	maxFetchTimeoutSec     = 300
	// maxFetchBytes is the largest file the grantor serves (8 MiB); a client
	// that is told more stops instead of buffering it.
	maxFetchBytes = capability.MaxFileBytes
	maxListPages  = 1000
)

const fetchUsage = `Read a file, a listing or a stat from a directory or git branch another agent granted you.

Usage:
  agentnet fetch <g-id> <path> [--out FILE] [--json] [--timeout SECONDS]
  agentnet fetch <g-id> --list [<dir>] [--json] [--timeout SECONDS]
  agentnet fetch <g-id> --stat <path> [--json] [--timeout SECONDS]

<g-id> is a grant you hold ('agentnet grants --held'). <path> is relative to the
grant's scope, slash-separated, without '..'.

Flags:
  --out FILE        write the file to FILE (mode 0600) instead of stdout
  --list            list a directory ("" or no argument = the root of the scope)
  --stat            show one entry: name, type, size
  --timeout SECONDS default 30, at most 300: how long to wait for the grantor
  --json            print machine-readable JSON on stdout:
                      read: {"ok":true,"path","size","commit"?,"data":"<base64>"}
                            (with --out the file is written and "data" is left out)
                      list: {"ok":true,"path","entries":[{"name","type","size"?}],"commit"?}
                      stat: {"ok":true,"path","entry":{...},"commit"?}
                    Failures print {"ok":false,"error":{"code","message"}}.

A read fetches the whole file in 256 KiB reads (files up to 8 MiB). Without --out
and --json the raw bytes go to stdout. The grantor checks the grant on every read,
so a revoked grant stops the fetch at once. An expired or ended grant fails here,
without contacting the grantor. Files under .git, symlinks, devices and FIFOs are
not served.

Errors: unknown_grant, expired, session_not_open, revoked, bad_path, out_of_scope,
not_found, symlink, not_regular, too_large, rate_limited, stale, io_error,
timeout, changed (the branch or file changed during the fetch: try again),
relay_unavailable, no_relay.

Exit codes: 0 ok, 1 error, 2 usage, 3 daemon not running, 4 timeout (the grantor
is offline or did not answer within --timeout).
`

type fetchReadResult struct {
	Data   string `json:"data"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	Commit string `json:"commit"`
}

type fetchListResult struct {
	Entries []capability.Entry `json:"entries"`
	Cursor  string             `json:"cursor"`
	Commit  string             `json:"commit"`
}

type fetchStatResult struct {
	Entry  capability.Entry `json:"entry"`
	Commit string           `json:"commit"`
}

func runFetch(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("agentnet fetch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print machine-readable JSON on stdout")
	out := fs.String("out", "", "write the file here")
	list := fs.Bool("list", false, "list a directory")
	stat := fs.Bool("stat", false, "show one entry")
	timeoutSec := fs.Int("timeout", defaultFetchTimeoutSec, "seconds to wait for the grantor")
	fs.Usage = func() { _, _ = fmt.Fprint(stdout, fetchUsage) }
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", err.Error())
	}
	if len(args) == 0 {
		_, _ = fmt.Fprint(stdout, fetchUsage)
		return exitOK
	}
	usage := func(msg string) int {
		return failJSON(*asJSON, stdout, stderr, exitUsage, "usage", msg+" (see 'agentnet fetch --help')")
	}
	if *list && *stat {
		return usage("--list and --stat exclude each other")
	}
	if *out != "" && (*list || *stat) {
		return usage("--out is for reading a file")
	}
	if *timeoutSec < 1 || *timeoutSec > maxFetchTimeoutSec {
		return usage("--timeout must be 1 to 300")
	}
	if len(pos) < 1 {
		return usage("give a grant id")
	}
	grantID, rest := pos[0], pos[1:]
	deadline := time.Now().Add(time.Duration(*timeoutSec) * time.Second)
	switch {
	case *list:
		if len(rest) > 1 {
			return usage("give at most one directory")
		}
		dir := ""
		if len(rest) == 1 {
			dir = rest[0]
		}
		return fetchList(*asJSON, stdout, stderr, deadline, grantID, dir)
	case *stat:
		if len(rest) != 1 {
			return usage("give exactly one path")
		}
		return fetchStat(*asJSON, stdout, stderr, deadline, grantID, rest[0])
	default:
		if len(rest) != 1 {
			return usage("give exactly one path")
		}
		return fetchFile(*asJSON, stdout, stderr, deadline, grantID, rest[0], *out)
	}
}

// fetchCall runs one fetch (fetch_start, then fetch_status until it is no
// longer pending) and returns its result, or the exit code after printing the
// failure. Each IPC call returns within 2 s (Docs/protocol/ipc.md); the loop
// gives up at the deadline.
func fetchCall(asJSON bool, stdout, stderr io.Writer, deadline time.Time, p daemon.FetchStartParams) (json.RawMessage, int) {
	remaining := time.Until(deadline)
	if remaining <= 0 {
		return nil, failJSON(asJSON, stdout, stderr, exitWaitTimeout, "timeout", "the grantor did not answer in time")
	}
	p.TimeoutS = max(1, int(math.Ceil(remaining.Seconds())))
	var st daemon.FetchStatus
	if code := callDaemon(asJSON, stdout, stderr, pairTimeout, "fetch_start", p, &st); code != exitOK {
		return nil, code
	}
	for st.State == "pending" {
		// The daemon ends the fetch at the deadline itself; this bound only
		// covers a daemon that stopped answering.
		if time.Now().After(deadline.Add(3 * time.Second)) {
			return nil, failJSON(asJSON, stdout, stderr, exitWaitTimeout, "timeout", "the grantor did not answer in time")
		}
		if code := callDaemon(asJSON, stdout, stderr, pairTimeout, "fetch_status", daemon.FetchStatusParams{FetchID: st.FetchID}, &st); code != exitOK {
			return nil, code
		}
	}
	if st.State == "failed" {
		code, msg := "io_error", "the fetch failed"
		if st.Error != nil {
			code, msg = st.Error.Code, st.Error.Message
		}
		exit := exitError
		if code == daemon.CodeFetchTimeout {
			exit = exitWaitTimeout
		}
		return nil, failJSON(asJSON, stdout, stderr, exit, code, msg)
	}
	return st.Result, exitOK
}

func fetchFile(asJSON bool, stdout, stderr io.Writer, deadline time.Time, grantID, path, outFile string) int {
	var data []byte
	var size int64
	var commit string
	for first := true; first || int64(len(data)) < size; first = false {
		raw, code := fetchCall(asJSON, stdout, stderr, deadline, daemon.FetchStartParams{
			Grant: grantID, Op: capability.OpRead, Path: path, Offset: int64(len(data)), Length: capability.MaxReadBytes,
		})
		if code != exitOK {
			return code
		}
		var r fetchReadResult
		if json.Unmarshal(raw, &r) != nil {
			return failJSON(asJSON, stdout, stderr, exitError, "io_error", "the daemon returned a malformed result")
		}
		chunk, err := base64.StdEncoding.DecodeString(r.Data)
		if err != nil {
			return failJSON(asJSON, stdout, stderr, exitError, "io_error", "the daemon returned malformed data")
		}
		if first {
			size, commit = r.Size, r.Commit
		} else if r.Size != size || r.Commit != commit {
			return failJSON(asJSON, stdout, stderr, exitError, "changed", "the file or branch changed during the fetch; try again")
		}
		if size > maxFetchBytes || (len(chunk) == 0 && int64(len(data)) < size) {
			return failJSON(asJSON, stdout, stderr, exitError, "io_error", "the grantor returned an unexpected size")
		}
		data = append(data, chunk...)
	}
	if outFile != "" {
		if err := writeFileAtomic(outFile, data); err != nil {
			return failJSON(asJSON, stdout, stderr, exitError, "io_error", fmt.Sprintf("cannot write %s: %v", outFile, err))
		}
	}
	switch {
	case asJSON:
		body := struct {
			OK     bool   `json:"ok"`
			Path   string `json:"path"`
			Size   int64  `json:"size"`
			Commit string `json:"commit,omitempty"`
			Data   string `json:"data,omitempty"`
		}{OK: true, Path: path, Size: size, Commit: commit}
		if outFile == "" {
			body.Data = base64.StdEncoding.EncodeToString(data)
		}
		_ = json.NewEncoder(stdout).Encode(body)
	case outFile != "":
		_, _ = fmt.Fprintf(stdout, "Wrote %d bytes to %s\n", size, outFile)
	default:
		_, _ = stdout.Write(data)
	}
	return exitOK
}

// writeFileAtomic writes data to a new mode-0600 file next to name and renames
// it over name, so a failure leaves no partial file, an existing file does not
// keep a wider mode, and a symlink at name is replaced rather than followed.
func writeFileAtomic(name string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(name), "."+filepath.Base(name)+".*.part")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp, name)
	}
	if err != nil {
		_ = os.Remove(tmp)
	}
	return err
}

// termSafe quotes s when it holds a character that is not printable, so that
// a name chosen by the grantor cannot send escape sequences to the terminal.
func termSafe(s string) string {
	if strings.IndexFunc(s, func(r rune) bool { return !unicode.IsPrint(r) }) >= 0 {
		return strconv.QuoteToGraphic(s)
	}
	return s
}

func fetchList(asJSON bool, stdout, stderr io.Writer, deadline time.Time, grantID, dir string) int {
	var entries []capability.Entry
	var commit, cursor string
	for page := 0; ; page++ {
		raw, code := fetchCall(asJSON, stdout, stderr, deadline, daemon.FetchStartParams{
			Grant: grantID, Op: capability.OpList, Path: dir, Cursor: cursor,
		})
		if code != exitOK {
			return code
		}
		var r fetchListResult
		if json.Unmarshal(raw, &r) != nil {
			return failJSON(asJSON, stdout, stderr, exitError, "io_error", "the daemon returned a malformed result")
		}
		if page > 0 && r.Commit != commit {
			return failJSON(asJSON, stdout, stderr, exitError, "changed", "the branch changed during the listing; try again")
		}
		commit = r.Commit
		entries = append(entries, r.Entries...)
		cursor = r.Cursor
		if cursor == "" {
			break
		}
		if page+1 >= maxListPages {
			return failJSON(asJSON, stdout, stderr, exitError, "io_error", "the listing has too many pages")
		}
	}
	if entries == nil {
		entries = []capability.Entry{}
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK      bool               `json:"ok"`
			Path    string             `json:"path"`
			Entries []capability.Entry `json:"entries"`
			Commit  string             `json:"commit,omitempty"`
		}{OK: true, Path: dir, Entries: entries, Commit: commit})
		return exitOK
	}
	tw := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tTYPE\tSIZE")
	for _, e := range entries {
		size := "-"
		if e.Size != nil {
			size = fmt.Sprint(*e.Size)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", termSafe(e.Name), termSafe(e.Type), size)
	}
	_ = tw.Flush()
	return exitOK
}

func fetchStat(asJSON bool, stdout, stderr io.Writer, deadline time.Time, grantID, path string) int {
	raw, code := fetchCall(asJSON, stdout, stderr, deadline, daemon.FetchStartParams{Grant: grantID, Op: capability.OpStat, Path: path})
	if code != exitOK {
		return code
	}
	var r fetchStatResult
	if json.Unmarshal(raw, &r) != nil {
		return failJSON(asJSON, stdout, stderr, exitError, "io_error", "the daemon returned a malformed result")
	}
	if asJSON {
		_ = json.NewEncoder(stdout).Encode(struct {
			OK     bool             `json:"ok"`
			Path   string           `json:"path"`
			Entry  capability.Entry `json:"entry"`
			Commit string           `json:"commit,omitempty"`
		}{OK: true, Path: path, Entry: r.Entry, Commit: r.Commit})
		return exitOK
	}
	size := "-"
	if r.Entry.Size != nil {
		size = fmt.Sprint(*r.Entry.Size)
	}
	_, _ = fmt.Fprintf(stdout, "%s\t%s\t%s\n", termSafe(r.Entry.Name), termSafe(r.Entry.Type), size)
	return exitOK
}
