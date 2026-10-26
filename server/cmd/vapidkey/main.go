// Command vapidkey creates the server's Web Push (VAPID) key pair.
//
//	go run ./cmd/vapidkey -out vapid.pem
//
// The private key is written to -out (PKCS #8 PEM, owner-only permissions;
// an existing file is never overwritten). Point TALKWIRE_VAPID_KEY_PATH at it.
// The public key is printed: browsers subscribe with it
// (PushManager.subscribe's applicationServerKey), and the server also logs it
// at startup.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"os"
)

func main() {
	out := flag.String("out", "", "file to write the private key to")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: vapidkey -out vapid.pem")
		os.Exit(2)
	}
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "vapidkey:", err)
		os.Exit(1)
	}
}

func run(path string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if err := pem.Encode(f, &pem.Block{Type: "PRIVATE KEY", Bytes: der}); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	pub, err := key.PublicKey.ECDH()
	if err != nil {
		return err
	}
	fmt.Println(base64.RawURLEncoding.EncodeToString(pub.Bytes()))
	return nil
}
