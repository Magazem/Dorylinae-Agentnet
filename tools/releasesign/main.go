// Command releasesign is the owner's offline release-signing tool (ticket
// 4.4a, D36 OD-P4-19 (a)+(ii)). CI publishes a DRAFT release with
// SHA256SUMS; the owner downloads SHA256SUMS, signs it here on their own
// machine with a key that is never in GitHub, uploads the two signature
// files to the draft and only then publishes it. See Docs/ops/release-signing.md.
//
// The signature is a plain Ed25519 signature over the exact bytes of
// SHA256SUMS (review 50 M8), written two ways from the same key:
//
//   - SHA256SUMS.sig: the raw 64-byte signature, checked by
//     `openssl pkeyutl -verify -rawin` (OpenSSL >= 3) in scripts/install.sh;
//   - SHA256SUMS.minisig: the same signature in minisign's legacy ("Ed",
//     not prehashed) format, checked by `minisign -V` where OpenSSL >= 3 is
//     missing (stock macOS ships LibreSSL).
//
// Usage:
//
//	go run ./tools/releasesign keygen -out KEYFILE
//	go run ./tools/releasesign pubkey -key KEYFILE
//	go run ./tools/releasesign embed -key KEYFILE -in scripts/install.sh -out scripts/install.sh
//	go run ./tools/releasesign sign -key KEYFILE SHA256SUMS
//	go run ./tools/releasesign verify (-pub PUBFILE | -install-sh scripts/install.sh) SHA256SUMS
//
// It uses only the Go standard library and imports no internal/ package.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

const usage = `releasesign: offline signing of a release's SHA256SUMS

Usage:
  releasesign keygen -out KEYFILE
  releasesign pubkey -key KEYFILE
  releasesign embed -key KEYFILE -in FILE -out FILE
  releasesign sign -key KEYFILE [-force] SHA256SUMS
  releasesign verify (-pub PUBFILE | -install-sh FILE) SHA256SUMS
  releasesign check SHA256SUMS   (format and version only; no signature)
`

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	var err error
	switch args[0] {
	case "keygen":
		err = cmdKeygen(args[1:], stdout)
	case "pubkey":
		err = cmdPubkey(args[1:], stdout)
	case "embed":
		err = cmdEmbed(args[1:], stdout)
	case "check":
		err = cmdCheck(args[1:], stdout)
	case "sign":
		err = cmdSign(args[1:], stdout)
	case "verify":
		err = cmdVerify(args[1:], stdout)
	case "-h", "--help", "help":
		_, _ = fmt.Fprint(stdout, usage)
		return 0
	default:
		_, _ = fmt.Fprint(stderr, usage)
		return 2
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "releasesign %s: %v\n", args[0], err)
		var ue usageError
		if errors.As(err, &ue) {
			return 2
		}
		return 1
	}
	return 0
}

type usageError struct{ msg string }

func (e usageError) Error() string { return e.msg }

func parse(name string, args []string, fs *flag.FlagSet, nargs int) error {
	fs.SetOutput(io.Discard)
	if err := fs.Parse(args); err != nil {
		return usageError{err.Error()}
	}
	if fs.NArg() != nargs {
		return usageError{fmt.Sprintf("%s takes %d file argument(s), got %d", name, nargs, fs.NArg())}
	}
	return nil
}

// --- keys ---

// keyID is minisign's 8-byte key id. minisign picks it at random; here it
// is derived from the public key so that no extra state has to be kept next
// to the key: the first 8 bytes of SHA-256("agentnet release key id" || pk).
func keyID(pub ed25519.PublicKey) []byte {
	h := sha256.Sum256(append([]byte("agentnet release key id\n"), pub...))
	return h[:8]
}

func readPrivateKey(path string) (ed25519.PrivateKey, error) {
	raw, err := os.ReadFile(path) //nolint:gosec // the owner names their own key file
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PRIVATE KEY" {
		return nil, errors.New("key file is not a PEM \"PRIVATE KEY\" (PKCS#8) block")
	}
	k, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	priv, ok := k.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("key file does not hold an Ed25519 key")
	}
	return priv, nil
}

func parsePublicPEM(raw []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil || block.Type != "PUBLIC KEY" {
		return nil, errors.New("no PEM \"PUBLIC KEY\" block")
	}
	k, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, err
	}
	pub, ok := k.(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("public key is not Ed25519")
	}
	return pub, nil
}

func publicPEM(pub ed25519.PublicKey) string {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		panic(err) // an ed25519.PublicKey always marshals
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}

// minisignPublic is the base64 line of a minisign public key ("Ed" || key
// id || pk), the value `minisign -V -P` takes.
func minisignPublic(pub ed25519.PublicKey) string {
	b := append(append([]byte("Ed"), keyID(pub)...), pub...)
	return base64.StdEncoding.EncodeToString(b)
}

func cmdKeygen(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("keygen", flag.ContinueOnError)
	out := fs.String("out", "", "file to write the new private key to (must not exist)")
	if err := parse("keygen", args, fs, 0); err != nil {
		return err
	}
	if *out == "" {
		return usageError{"-out is required"}
	}
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(*out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // the owner names the file
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Wrote the private key to %s. Keep it OFFLINE (never in GitHub), with a sealed backup copy.\n\n", *out)
	printPublic(stdout, pub)
	return nil
}

func cmdPubkey(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("pubkey", flag.ContinueOnError)
	keyFile := fs.String("key", "", "private key file")
	if err := parse("pubkey", args, fs, 0); err != nil {
		return err
	}
	priv, err := readPrivateKey(*keyFile)
	if err != nil {
		return err
	}
	printPublic(stdout, priv.Public().(ed25519.PublicKey))
	return nil
}

func printPublic(w io.Writer, pub ed25519.PublicKey) {
	_, _ = fmt.Fprintf(w, "Public key (PEM) -> scripts/install.sh AGENTNET_PUBKEY_PEM:\n%s\n", publicPEM(pub))
	_, _ = fmt.Fprintf(w, "minisign public key -> scripts/install.sh AGENTNET_MINISIGN_PUBKEY:\n%s\n", minisignPublic(pub))
}

// --- SHA256SUMS ---

// sumsLine is one line of SHA256SUMS as release.yml writes it (sha256sum
// format, two spaces, LF endings).
var sumsLine = regexp.MustCompile(`^([0-9a-f]{64})  agentnet_(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})\.(0|[1-9][0-9]{0,8})_(darwin|linux|windows)_(amd64|arm64)\.(tar\.gz|zip)$`)

// targets are the six archives every release carries.
var targets = []string{"darwin_amd64", "darwin_arm64", "linux_amd64", "linux_arm64", "windows_amd64", "windows_arm64"}

// checkSums validates SHA256SUMS before it is signed or after it is
// verified: exactly the six targets, one version, the right archive type
// per OS. It returns the version.
func checkSums(b []byte) (string, error) {
	if len(b) == 0 || b[len(b)-1] != '\n' {
		return "", errors.New("SHA256SUMS must end with a newline")
	}
	seen := map[string]bool{}
	version := ""
	for i, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		m := sumsLine.FindStringSubmatch(line)
		if m == nil {
			return "", fmt.Errorf("line %d is not \"<sha256>  agentnet_<X.Y.Z>_<os>_<arch>.<tar.gz|zip>\": %q", i+1, line)
		}
		v := m[2] + "." + m[3] + "." + m[4]
		if version == "" {
			version = v
		} else if v != version {
			return "", fmt.Errorf("line %d names version %s, earlier lines %s", i+1, v, version)
		}
		if (m[5] == "windows") != (m[7] == "zip") {
			return "", fmt.Errorf("line %d: windows archives are .zip, others .tar.gz", i+1)
		}
		t := m[5] + "_" + m[6]
		if seen[t] {
			return "", fmt.Errorf("line %d: %s listed twice", i+1, t)
		}
		seen[t] = true
	}
	for _, t := range targets {
		if !seen[t] {
			return "", fmt.Errorf("no archive for %s", t)
		}
	}
	return version, nil
}

// --- signatures ---

// minisig renders a legacy-format minisign signature over msg.
func minisig(priv ed25519.PrivateKey, msg []byte, trusted string) []byte {
	pub := priv.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(priv, msg)
	sigLine := base64.StdEncoding.EncodeToString(append(append([]byte("Ed"), keyID(pub)...), sig...))
	global := ed25519.Sign(priv, append(append([]byte{}, sig...), trusted...))
	return []byte("untrusted comment: agentnet release signature (plain Ed25519 over SHA256SUMS)\n" +
		sigLine + "\ntrusted comment: " + trusted + "\n" +
		base64.StdEncoding.EncodeToString(global) + "\n")
}

// verifyMinisig checks a legacy-format minisign signature the way
// `minisign -V` does: algorithm "Ed", key id, signature over msg, and the
// global signature over signature || trusted comment.
func verifyMinisig(pub ed25519.PublicKey, msg, file []byte) error {
	lines := strings.Split(strings.TrimRight(string(file), "\n"), "\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[0], "untrusted comment: ") || !strings.HasPrefix(lines[2], "trusted comment: ") {
		return errors.New("minisig: not a 4-line minisign signature")
	}
	raw, err := base64.StdEncoding.DecodeString(lines[1])
	if err != nil || len(raw) != 2+8+ed25519.SignatureSize {
		return errors.New("minisig: bad signature line")
	}
	if string(raw[:2]) != "Ed" {
		return errors.New("minisig: not a legacy (\"Ed\") signature")
	}
	if !bytes.Equal(raw[2:10], keyID(pub)) {
		return errors.New("minisig: key id does not match")
	}
	sig := raw[10:]
	if !ed25519.Verify(pub, msg, sig) {
		return errors.New("minisig: signature does not verify")
	}
	global, err := base64.StdEncoding.DecodeString(lines[3])
	if err != nil || !ed25519.Verify(pub, append(append([]byte{}, sig...), strings.TrimPrefix(lines[2], "trusted comment: ")...), global) {
		return errors.New("minisig: trusted comment signature does not verify")
	}
	return nil
}

// cmdCheck validates SHA256SUMS's format (release.yml runs it before
// publishing the draft) and prints the version it names.
func cmdCheck(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	if err := parse("check", args, fs, 1); err != nil {
		return err
	}
	sums, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return err
	}
	version, err := checkSums(sums)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintln(stdout, version)
	return nil
}

func cmdSign(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("sign", flag.ContinueOnError)
	keyFile := fs.String("key", "", "private key file")
	force := fs.Bool("force", false, "overwrite existing signature files")
	if err := parse("sign", args, fs, 1); err != nil {
		return err
	}
	sumsPath := fs.Arg(0)
	priv, err := readPrivateKey(*keyFile)
	if err != nil {
		return err
	}
	sums, err := os.ReadFile(sumsPath) //nolint:gosec // the owner names the file
	if err != nil {
		return err
	}
	version, err := checkSums(sums)
	if err != nil {
		return fmt.Errorf("refusing to sign: %w", err)
	}
	pub := priv.Public().(ed25519.PublicKey)
	sig := ed25519.Sign(priv, sums)
	ms := minisig(priv, sums, "agentnet "+version+" SHA256SUMS")
	if !ed25519.Verify(pub, sums, sig) || verifyMinisig(pub, sums, ms) != nil {
		return errors.New("self-check of the new signatures failed")
	}
	if err := writeOut(sumsPath+".sig", sig, *force); err != nil {
		return err
	}
	if err := writeOut(sumsPath+".minisig", ms, *force); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Signed agentnet %s: wrote %s.sig and %s.minisig\n", version, sumsPath, sumsPath)
	return nil
}

func writeOut(path string, b []byte, force bool) error {
	flags := os.O_WRONLY | os.O_CREATE | os.O_EXCL
	if force {
		flags = os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	}
	f, err := os.OpenFile(path, flags, 0o644) //nolint:gosec // public signature files
	if err != nil {
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// installShKey extracts the PEM public key and the minisign public key
// embedded in scripts/install.sh.
func installShKey(raw []byte) (ed25519.PublicKey, string, error) {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(s, "AGENTNET_PUBKEY_PEM='")
	if start < 0 {
		return nil, "", errors.New("no AGENTNET_PUBKEY_PEM='...' in the script")
	}
	rest := s[start+len("AGENTNET_PUBKEY_PEM='"):]
	end := strings.Index(rest, "'")
	if end < 0 {
		return nil, "", errors.New("unterminated AGENTNET_PUBKEY_PEM")
	}
	pub, err := parsePublicPEM([]byte(rest[:end]))
	if err != nil {
		return nil, "", fmt.Errorf("AGENTNET_PUBKEY_PEM: %w (still the placeholder?)", err)
	}
	m := regexp.MustCompile(`(?m)^AGENTNET_MINISIGN_PUBKEY='([^']*)'$`).FindStringSubmatch(s)
	if m == nil {
		return nil, "", errors.New("no AGENTNET_MINISIGN_PUBKEY='...' line in the script")
	}
	return pub, m[1], nil
}

// pemLine and minisignLine match the two key lines of install.sh, whether
// they still hold the placeholder or an earlier key.
var (
	pemLine      = regexp.MustCompile(`(?ms)^AGENTNET_PUBKEY_PEM='[^']*'$`)
	minisignLine = regexp.MustCompile(`(?m)^AGENTNET_MINISIGN_PUBKEY='[^']*'$`)
)

// cmdEmbed writes the public half of KEYFILE into install.sh's two key
// lines. Only public values are written.
func cmdEmbed(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("embed", flag.ContinueOnError)
	keyFile := fs.String("key", "", "private key file (only its public half is written)")
	in := fs.String("in", "", "install.sh to read")
	out := fs.String("out", "", "file to write (may be the same as -in)")
	if err := parse("embed", args, fs, 0); err != nil {
		return err
	}
	if *in == "" || *out == "" {
		return usageError{"-in and -out are required"}
	}
	priv, err := readPrivateKey(*keyFile)
	if err != nil {
		return err
	}
	pub := priv.Public().(ed25519.PublicKey)
	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	if len(pemLine.FindAllStringIndex(s, -1)) != 1 || len(minisignLine.FindAllStringIndex(s, -1)) != 1 {
		return errors.New("expected exactly one AGENTNET_PUBKEY_PEM='...' and one AGENTNET_MINISIGN_PUBKEY='...' line")
	}
	pemText := strings.TrimSuffix(publicPEM(pub), "\n")
	s = pemLine.ReplaceAllLiteralString(s, "AGENTNET_PUBKEY_PEM='"+pemText+"'")
	s = minisignLine.ReplaceAllLiteralString(s, "AGENTNET_MINISIGN_PUBKEY='"+minisignPublic(pub)+"'")
	if err := os.WriteFile(*out, []byte(s), 0o755); err != nil { //nolint:gosec // a script
		return err
	}
	_, _ = fmt.Fprintf(stdout, "Wrote the release public key into %s\n", *out)
	return nil
}

func cmdVerify(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	pubFile := fs.String("pub", "", "PEM public key file")
	script := fs.String("install-sh", "", "install.sh whose embedded keys to verify against")
	if err := parse("verify", args, fs, 1); err != nil {
		return err
	}
	if (*pubFile == "") == (*script == "") {
		return usageError{"give exactly one of -pub and -install-sh"}
	}
	var pub ed25519.PublicKey
	if *pubFile != "" {
		raw, err := os.ReadFile(*pubFile)
		if err != nil {
			return err
		}
		if pub, err = parsePublicPEM(raw); err != nil {
			return err
		}
	} else {
		raw, err := os.ReadFile(*script)
		if err != nil {
			return err
		}
		var mpub string
		if pub, mpub, err = installShKey(raw); err != nil {
			return err
		}
		if mpub != minisignPublic(pub) {
			return errors.New("install.sh: AGENTNET_MINISIGN_PUBKEY is not the same key as AGENTNET_PUBKEY_PEM")
		}
	}
	sumsPath := fs.Arg(0)
	sums, err := os.ReadFile(sumsPath) //nolint:gosec // the owner names the file
	if err != nil {
		return err
	}
	sig, err := os.ReadFile(sumsPath + ".sig") //nolint:gosec // next to the named file
	if err != nil {
		return err
	}
	if !ed25519.Verify(pub, sums, sig) {
		return errors.New(sumsPath + ".sig does not verify")
	}
	ms, err := os.ReadFile(sumsPath + ".minisig") //nolint:gosec // next to the named file
	if err != nil {
		return err
	}
	if err := verifyMinisig(pub, sums, ms); err != nil {
		return err
	}
	version, err := checkSums(sums)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(stdout, "OK: agentnet %s SHA256SUMS, .sig and .minisig verify\n", version)
	return nil
}
