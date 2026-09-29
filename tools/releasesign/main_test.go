package main

import (
	"bytes"
	"crypto/ed25519"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func fakeSums(version string) []byte {
	var b strings.Builder
	for i, t := range targets {
		ext := "tar.gz"
		if strings.HasPrefix(t, "windows") {
			ext = "zip"
		}
		fmt.Fprintf(&b, "%064x  agentnet_%s_%s.%s\n", i+1, version, t, ext)
	}
	return []byte(b.String())
}

// realRelease writes six small archives and their SHA256SUMS into dir and
// returns the SHA256SUMS path and its SHA-256.
func realRelease(t *testing.T, dir, version string) (string, string) {
	t.Helper()
	sums, archives := releaseFiles(version, "")
	for name, b := range archives {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(p, sums, 0o600); err != nil {
		t.Fatal(err)
	}
	return p, sha256Of(sums)
}

// releaseFiles returns a SHA256SUMS and the six archives it names; salt
// changes every archive's bytes.
func releaseFiles(version, salt string) ([]byte, map[string][]byte) {
	archives := map[string][]byte{}
	var b strings.Builder
	for _, t := range targets {
		ext := "tar.gz"
		if strings.HasPrefix(t, "windows") {
			ext = "zip"
		}
		name := fmt.Sprintf("agentnet_%s_%s.%s", version, t, ext)
		archives[name] = []byte("archive " + name + salt)
		b.WriteString(sha256Of(archives[name]) + "  " + name + "\n")
	}
	return []byte(b.String()), archives
}

func runOK(t *testing.T, args ...string) string {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code != 0 {
		t.Fatalf("releasesign %v: exit %d: %s", args, code, errb.String())
	}
	return out.String()
}

func runFail(t *testing.T, want string, args ...string) {
	t.Helper()
	var out, errb bytes.Buffer
	if code := run(args, &out, &errb); code == 0 {
		t.Fatalf("releasesign %v succeeded, want failure", args)
	}
	if !strings.Contains(errb.String(), want) {
		t.Fatalf("releasesign %v: stderr %q, want %q", args, errb.String(), want)
	}
}

// placeholderInstallSh returns scripts/install.sh with both key assignments put
// back to the unreleased placeholders, whatever key the repo file embeds.
func placeholderInstallSh(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if len(pemLine.FindAllStringIndex(s, -1)) != 1 || len(minisignLine.FindAllStringIndex(s, -1)) != 1 {
		t.Fatal("install.sh: expected exactly one PEM and one minisign key assignment")
	}
	s = pemLine.ReplaceAllLiteralString(s, "AGENTNET_PUBKEY_PEM='REPLACE_WITH_RELEASE_PUBLIC_KEY_PEM'")
	s = minisignLine.ReplaceAllLiteralString(s, "AGENTNET_MINISIGN_PUBKEY='REPLACE_WITH_RELEASE_MINISIGN_PUBKEY'")
	return []byte(s)
}

// The repo's install.sh must carry a real release key (both encodings of the
// same key), never the placeholders.
func TestRepoInstallShHasRealKey(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "scripts", "install.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// The script's own guard mentions the placeholders, so look at the
	// assignments only.
	if strings.Contains(string(raw), "_PUBKEY_PEM='REPLACE_WITH_RELEASE") ||
		strings.Contains(string(raw), "_MINISIGN_PUBKEY='REPLACE_WITH_RELEASE") {
		t.Fatal("scripts/install.sh still holds a REPLACE_WITH_RELEASE placeholder")
	}
	pub, mpub, err := installShKey(raw)
	if err != nil {
		t.Fatal(err)
	}
	if mpub != minisignPublic(pub) {
		t.Fatal("scripts/install.sh: the minisign key is not the same key as the PEM key")
	}
}

func TestKeygenSignVerifyEmbed(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "release.key")
	runOK(t, "keygen", "-out", key)
	runFail(t, "exists", "keygen", "-out", key) // never overwrites a key

	sums, digest := realRelease(t, dir, "1.2.3")
	if out := runOK(t, "sign", "-key", key, "-expect-sha256", digest, "-archives", dir, sums); !strings.Contains(out, "1.2.3") {
		t.Fatalf("sign output %q does not name the version", out)
	}
	runFail(t, "exists", "sign", "-key", key, "-expect-sha256", digest, "-archives", dir, sums) // no silent overwrite
	runOK(t, "sign", "-key", key, "-expect-sha256", digest, "-archives", dir, "-force", sums)

	// embed into a placeholder copy of install.sh and verify against it.
	script := placeholderInstallSh(t)
	placeholder := filepath.Join(dir, "placeholder.sh")
	if err := os.WriteFile(placeholder, script, 0o600); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	runFail(t, "placeholder", "verify", "-install-sh", placeholder, sums)
	embedded := filepath.Join(dir, "install.sh")
	runOK(t, "embed", "-key", key, "-in", placeholder, "-out", embedded)
	if out := runOK(t, "verify", "-install-sh", embedded, sums); !strings.Contains(out, "OK") {
		t.Fatalf("verify output %q", out)
	}
	got, _ := os.ReadFile(embedded) //nolint:gosec // test temp dir
	if strings.Contains(string(got), "='REPLACE_WITH_RELEASE") {
		t.Fatal("embed left a placeholder behind")
	}
	if bytes.Contains(got, []byte("PRIVATE")) {
		t.Fatal("embed wrote private key material")
	}
	// Re-embedding a different key replaces the old one.
	key2 := filepath.Join(dir, "other.key")
	runOK(t, "keygen", "-out", key2)
	runOK(t, "embed", "-key", key2, "-in", embedded, "-out", embedded)
	runFail(t, "does not verify", "verify", "-install-sh", embedded, sums)

	// A flipped byte in SHA256SUMS breaks both signatures.
	pub := filepath.Join(dir, "release.pub")
	priv, err := readPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pub, []byte(publicPEM(priv.Public().(ed25519.PublicKey))), 0o600); err != nil {
		t.Fatal(err)
	}
	runOK(t, "verify", "-pub", pub, sums)
	b, _ := os.ReadFile(sums) //nolint:gosec // test temp dir
	b[3] ^= 1
	if err := os.WriteFile(sums, b, 0o600); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	runFail(t, "does not verify", "verify", "-pub", pub, sums)
}

func TestSignRefusesMalformedSums(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	runOK(t, "keygen", "-out", key)
	good := string(fakeSums("1.2.3"))
	lines := strings.Split(strings.TrimSuffix(good, "\n"), "\n")
	for name, body := range map[string]string{
		"crlf":          strings.ReplaceAll(good, "\n", "\r\n"),
		"no newline":    strings.TrimSuffix(good, "\n"),
		"two versions":  strings.Replace(good, "1.2.3_linux_amd64", "1.2.4_linux_amd64", 1),
		"missing arch":  strings.Join(lines[1:], "\n") + "\n",
		"duplicate":     good + lines[0] + "\n",
		"extra file":    good + strings.Repeat("a", 64) + "  evil.sh\n",
		"windows tar":   strings.Replace(good, "windows_amd64.zip", "windows_amd64.tar.gz", 1),
		"leading zero":  strings.ReplaceAll(good, "_1.2.3_", "_01.2.3_"),
		"prerelease":    strings.ReplaceAll(good, "_1.2.3_", "_1.2.3-rc1_"),
		"one space":     strings.Replace(good, "  agentnet", " agentnet", 1),
		"upper hex sum": strings.Replace(good, "0000000001", "000000000A", 1),
	} {
		p := filepath.Join(dir, "SUMS-"+strings.ReplaceAll(name, " ", "-"))
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		// The digest matches, so only the format check can refuse.
		var out, errb bytes.Buffer
		if code := run([]string{"sign", "-key", key, "-expect-sha256", sha256Of([]byte(body)), "-archives", dir, p}, &out, &errb); code == 0 {
			t.Errorf("%s: signed a malformed SHA256SUMS", name)
		} else if !strings.Contains(errb.String(), "format check") {
			t.Errorf("%s: refused for another reason: %s", name, errb.String())
		}
		if _, err := os.Stat(p + ".sig"); err == nil {
			t.Errorf("%s: wrote a signature anyway", name)
		}
	}
}

// The raw signature must be what `openssl pkeyutl -verify -rawin` accepts
// (install.sh's first path). Skipped without OpenSSL 3 on PATH.
func TestOpenSSLInterop(t *testing.T) {
	v, err := exec.Command("openssl", "version").Output()
	if err != nil || !strings.HasPrefix(string(v), "OpenSSL 3") {
		t.Skip("OpenSSL 3 not available")
	}
	dir := t.TempDir()
	key := filepath.Join(dir, "k")
	runOK(t, "keygen", "-out", key)
	sums, digest := realRelease(t, dir, "0.4.0")
	runOK(t, "sign", "-key", key, "-expect-sha256", digest, "-archives", dir, sums)
	priv, err := readPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pub := filepath.Join(dir, "pub.pem")
	if err := os.WriteFile(pub, []byte(publicPEM(priv.Public().(ed25519.PublicKey))), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("openssl", "pkeyutl", "-verify", "-pubin", "-inkey", pub, "-rawin", "-in", sums, "-sigfile", sums+".sig").CombinedOutput() //nolint:gosec // test paths
	if err != nil || !strings.Contains(string(out), "Signature Verified Successfully") {
		t.Fatalf("openssl verify: %v: %s", err, out)
	}
	// And the key file is usable by openssl for signing, giving the same
	// bytes (Ed25519 is deterministic): the owner can cross-check by hand.
	osig := filepath.Join(dir, "openssl.sig")
	if out, err := exec.Command("openssl", "pkeyutl", "-sign", "-inkey", key, "-rawin", "-in", sums, "-out", osig).CombinedOutput(); err != nil { //nolint:gosec // test paths
		t.Fatalf("openssl sign: %v: %s", err, out)
	}
	a, _ := os.ReadFile(osig)          //nolint:gosec // test temp dir
	b, _ := os.ReadFile(sums + ".sig") //nolint:gosec // test temp dir
	if !bytes.Equal(a, b) {
		t.Fatal("openssl and releasesign signatures differ")
	}
}

// --- R55-F3: sign is bound to the CI run (spec 57 §7 A1-A5) ---

// runCode runs releasesign and returns its exit code and stderr.
func runCode(args ...string) (int, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, errb.String()
}

func assertNoSignature(t *testing.T, sums string) {
	t.Helper()
	for _, ext := range []string{".sig", ".minisig"} {
		if _, err := os.Stat(sums + ext); err == nil {
			t.Fatalf("a refused sign still wrote %s%s", sums, ext)
		}
	}
}

// c1501Sums is review 55's C15-01 fixture: a well-formed SHA256SUMS whose
// hashes belong to no build (an attacker's swap of the draft asset).
func c1501Sums(t *testing.T, dir string) string {
	t.Helper()
	var b strings.Builder
	for _, tg := range targets {
		ext := "tar.gz"
		if strings.HasPrefix(tg, "windows") {
			ext = "zip"
		}
		b.WriteString(strings.Repeat("e", 64) + "  agentnet_1.2.3_" + tg + "." + ext + "\n")
	}
	sums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return sums
}

// A1 (C15-01 inverted): the runbook's old call no longer signs, and neither
// does a call that leaves out -expect-sha256; the error names the flag
// (today's flag package would also give rc 2, for an unknown flag).
func TestSignRefusesUnboundSums(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k.pem")
	runOK(t, "keygen", "-out", key)
	sums := c1501Sums(t, dir)

	for name, args := range map[string][]string{
		"old runbook call":         {"sign", "-key", key, sums},
		"archives, no digest":      {"sign", "-key", key, "-archives", dir, sums},
		"archives, empty digest":   {"sign", "-key", key, "-archives", dir, "-expect-sha256", "", sums},
		"digest only, no archives": {"sign", "-key", key, "-expect-sha256", strings.Repeat("a", 64), sums},
	} {
		code, stderr := runCode(args...)
		want := "-expect-sha256 is required"
		if name == "digest only, no archives" {
			want = "-archives is required"
		}
		if code != 2 || !strings.Contains(stderr, want) {
			t.Errorf("%s: rc=%d stderr=%q, want rc 2 and %q", name, code, stderr, want)
		}
		assertNoSignature(t, sums)
	}
}

// A2: a well-formed SHA256SUMS that is not the one the run logged.
func TestSignRefusesDigestMismatch(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k.pem")
	runOK(t, "keygen", "-out", key)
	sums := c1501Sums(t, dir)
	raw, err := os.ReadFile(sums) //nolint:gosec // test temp dir
	if err != nil {
		t.Fatal(err)
	}
	logged := sha256Of(fakeSums("1.2.3")) // the digest of a different well-formed SUMS
	code, stderr := runCode("sign", "-key", key, "-expect-sha256", logged, "-archives", dir, sums)
	if code != 1 || !strings.Contains(stderr, "digest check") ||
		!strings.Contains(stderr, logged) || !strings.Contains(stderr, sha256Of(raw)) {
		t.Fatalf("rc=%d stderr=%q, want rc 1 naming the digest check, expected and actual digests", code, stderr)
	}
	assertNoSignature(t, sums)
}

// A3: -expect-sha256 must be exactly 64 lowercase hex.
func TestSignRefusesBadExpectFormat(t *testing.T) {
	dir := t.TempDir()
	key := filepath.Join(dir, "k.pem")
	runOK(t, "keygen", "-out", key)
	sums, digest := realRelease(t, dir, "1.2.3")
	for name, v := range map[string]string{
		"uppercase": strings.ToUpper(digest),
		"63 hex":    digest[:63],
		"65 hex":    digest + "0",
		"prefix":    "sha256:" + digest,
		"suffix":    digest + " ",
		"not hex":   strings.Repeat("g", 64),
	} {
		code, stderr := runCode("sign", "-key", key, "-expect-sha256", v, "-archives", dir, sums)
		if code != 2 || !strings.Contains(stderr, "must be a SHA-256 digest") {
			t.Errorf("%s: rc=%d stderr=%q, want rc 2 naming the digest format", name, code, stderr)
		}
		assertNoSignature(t, sums)
	}
}

// A4: the archives in -archives must be exactly the six SHA256SUMS names,
// each matching its line.
func TestSignArchives(t *testing.T) {
	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "k.pem")
	runOK(t, "keygen", "-out", key)
	linux := "agentnet_1.2.3_linux_amd64.tar.gz"
	for name, tc := range map[string]struct {
		change func(dir string) error
		want   string
	}{
		"all good": {func(string) error { return nil }, ""},
		"flipped byte": {func(dir string) error {
			p := filepath.Join(dir, linux)
			b, err := os.ReadFile(p) //nolint:gosec // test temp dir
			if err != nil {
				return err
			}
			b[0] ^= 1
			return os.WriteFile(p, b, 0o600) //nolint:gosec // test temp dir
		}, linux + " has SHA-256"},
		"missing": {func(dir string) error { return os.Remove(filepath.Join(dir, linux)) }, linux + " is missing"},
		"extra agentnet file": {func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "agentnet_1.2.3_linux_amd64.exe"), []byte("x"), 0o600)
		}, "agentnet_1.2.3_linux_amd64.exe is not named in SHA256SUMS"},
		"empty folder": {func(dir string) error {
			for _, e := range must(os.ReadDir(dir)) {
				if strings.HasPrefix(e.Name(), "agentnet_") {
					if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
						return err
					}
				}
			}
			return nil
		}, "is missing"},
	} {
		dir := t.TempDir()
		sums, digest := realRelease(t, dir, "1.2.3")
		if err := tc.change(dir); err != nil {
			t.Fatal(err)
		}
		code, stderr := runCode("sign", "-key", key, "-expect-sha256", digest, "-archives", dir, sums)
		if tc.want == "" {
			if code != 0 {
				t.Errorf("%s: rc=%d stderr=%q, want a signature", name, code, stderr)
			}
			continue
		}
		if code != 1 || !strings.Contains(stderr, "archive check") || !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: rc=%d stderr=%q, want rc 1 and %q", name, code, stderr, tc.want)
		}
		assertNoSignature(t, sums)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// A5: the bound signature verifies against install.sh's embedded key, and
// verify -archives checks the published files.
func TestSignBoundHappyPath(t *testing.T) {
	dir := t.TempDir()
	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "k.pem")
	runOK(t, "keygen", "-out", key)
	sums, digest := realRelease(t, dir, "1.2.3")
	runOK(t, "sign", "-key", key, "-expect-sha256", digest, "-archives", dir, sums)

	placeholder := filepath.Join(keyDir, "placeholder.sh")
	if err := os.WriteFile(placeholder, placeholderInstallSh(t), 0o600); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
	script := filepath.Join(keyDir, "install.sh")
	runOK(t, "embed", "-key", key, "-in", placeholder, "-out", script)
	runOK(t, "verify", "-install-sh", script, sums)
	if out := runOK(t, "verify", "-install-sh", script, "-archives", dir, sums); !strings.Contains(out, "archives") {
		t.Fatalf("verify -archives output %q", out)
	}
	// A published archive swapped after signing fails verify -archives.
	p := filepath.Join(dir, "agentnet_1.2.3_darwin_arm64.tar.gz")
	if err := os.WriteFile(p, []byte("swapped"), 0o600); err != nil {
		t.Fatal(err)
	}
	runFail(t, "agentnet_1.2.3_darwin_arm64.tar.gz has SHA-256", "verify", "-install-sh", script, "-archives", dir, sums)
}
