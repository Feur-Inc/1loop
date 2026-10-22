// Command 1loop-release signs 1loop's update manifest (internal/update).
//
//	1loop-release keygen KEY.pem
//	    makes the release key; prints the public key to put in
//	    internal/update and the phone app.
//	1loop-release manifest -key KEY.pem -version 1.2.3 -base URL -page URL -notes FILE platform=FILE...
//	    prints the signed latest.json for those files, served under URL.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"1loop/internal/update"
)

// inAppEnd ends the part of the release notes shown in the apps; invisible
// on GitHub.
const inAppEnd = "<!-- end of the in-app notes -->"

func main() {
	if len(os.Args) < 2 {
		fail("usage: 1loop-release keygen KEY.pem | manifest …")
	}
	switch os.Args[1] {
	case "keygen":
		if len(os.Args) != 3 {
			fail("usage: 1loop-release keygen KEY.pem")
		}
		keygen(os.Args[2])
	case "manifest":
		manifest(os.Args[2:])
	default:
		fail("unknown command " + os.Args[1])
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}

func keygen(path string) {
	if _, err := os.Stat(path); err == nil {
		fail(path + " exists: a release key is never replaced by accident")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		fail(err.Error())
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		fail(err.Error())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		fail(err.Error())
	}
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}), 0600); err != nil {
		fail(err.Error())
	}
	public, _ := x509.MarshalPKIXPublicKey(&key.PublicKey)
	os.Stdout.Write(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: public}))
}

func manifest(args []string) {
	flags := flag.NewFlagSet("manifest", flag.ExitOnError)
	keyPath := flags.String("key", "", "the release key")
	version := flags.String("version", "", "the release's version, x.y.z")
	base := flags.String("base", "", "the URL the files are served under")
	page := flags.String("page", "", "the release page")
	notesPath := flags.String("notes", "", "a file with the release notes")
	flags.Parse(args)
	if *keyPath == "" || update.Code(*version) == 0 || *base == "" || flags.NArg() == 0 {
		fail("manifest needs -key, -version x.y.z, -base and platform=FILE arguments")
	}
	data, err := os.ReadFile(*keyPath)
	if err != nil {
		fail(err.Error())
	}
	block, _ := pem.Decode(data)
	if block == nil {
		fail("no key in " + *keyPath)
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	key, ok := parsed.(*ecdsa.PrivateKey)
	if err != nil || !ok {
		fail("not an ECDSA release key")
	}
	notes := ""
	if *notesPath != "" {
		raw, err := os.ReadFile(*notesPath)
		if err != nil {
			fail(err.Error())
		}
		// The apps show the notes down to this line; the rest (install steps)
		// is for the release page.
		notes, _, _ = strings.Cut(string(raw), inAppEnd)
		notes = strings.TrimSpace(notes)
	}
	out := update.Manifest{Version: *version, Code: update.Code(*version), Date: time.Now().UTC().Format("2006-01-02"),
		Notes: notes, Page: *page, Files: map[string]update.File{}}
	for _, argument := range flags.Args() {
		platform, path, found := strings.Cut(argument, "=")
		if !found {
			fail("expected platform=FILE, got " + argument)
		}
		file, err := os.Open(path)
		if err != nil {
			fail(err.Error())
		}
		hasher := sha256.New()
		size, err := io.Copy(hasher, file)
		file.Close()
		if err != nil {
			fail(err.Error())
		}
		out.Files[platform] = update.File{URL: strings.TrimRight(*base, "/") + "/" + filepath.Base(path), SHA256: hex.EncodeToString(hasher.Sum(nil)), Size: size}
	}
	payload, _ := json.Marshal(out)
	digest := sha256.Sum256(payload)
	signature, err := ecdsa.SignASN1(rand.Reader, key, digest[:])
	if err != nil {
		fail(err.Error())
	}
	envelope, _ := json.MarshalIndent(map[string]string{
		"payload":   base64.StdEncoding.EncodeToString(payload),
		"signature": base64.StdEncoding.EncodeToString(signature),
	}, "", "  ")
	os.Stdout.Write(append(envelope, '\n'))
}
