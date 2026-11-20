// Package web embeds locales and static assets of the UI.
package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
)

// FS holds locales/*.json and static/*.
//
//go:embed locales static
var FS embed.FS

// Version is a hash of the static files. Pages add it to asset URLs as ?v= so browsers,
// which cache static files, load them anew once they change.
var Version = version()

func version() string {
	h := sha256.New()
	err := fs.WalkDir(FS, "static", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := FS.ReadFile(path)
		if err != nil {
			return err
		}
		h.Write([]byte(path))
		h.Write(b)
		return nil
	})
	if err != nil {
		panic("hash static files: " + err.Error())
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
