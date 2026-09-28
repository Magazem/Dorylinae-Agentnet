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

	sums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, fakeSums("1.2.3"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out := runOK(t, "sign", "-key", key, sums); !strings.Contains(out, "1.2.3") {
		t.Fatalf("sign output %q does not name the version", out)
	}
	runFail(t, "exists", "sign", "-key", key, sums) // no silent overwrite
	runOK(t, "sign", "-key", key, "-force", sums)

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
		var out, errb bytes.Buffer
		if code := run([]string{"sign", "-key", key, p}, &out, &errb); code == 0 {
			t.Errorf("%s: signed a malformed SHA256SUMS", name)
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
	sums := filepath.Join(dir, "SHA256SUMS")
	if err := os.WriteFile(sums, fakeSums("0.4.0"), 0o600); err != nil {
		t.Fatal(err)
	}
	runOK(t, "sign", "-key", key, sums)
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
